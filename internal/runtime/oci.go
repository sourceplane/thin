package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// PullProviderOCI pulls a provider from an OCI registry and extracts platform-specific files.
// Uses oras.Copy for efficient, concurrent layer downloads.
func PullProviderOCI(ctx context.Context, imageRef string, providerName string) error {
	providerBaseDir := filepath.Join(ThinHome(), "providers", providerName)
	if err := os.MkdirAll(providerBaseDir, 0755); err != nil {
		return fmt.Errorf("failed to create provider directory: %w", err)
	}

	handler := NewStatusHandler()
	defer handler.Close()

	fmt.Printf("Downloading %s from %s...\n", providerName, imageRef)

	// Normalize image reference
	ref := imageRef
	if !strings.Contains(ref, "/") {
		ref = "docker.io/" + ref
	}
	if !strings.Contains(ref, ":") && !strings.Contains(ref, "@") {
		ref = ref + ":latest"
	}

	// Connect to registry
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return fmt.Errorf("failed to parse image reference %s: %w", ref, err)
	}

	// Optimized HTTP transport — no Client.Timeout (it kills in-flight body reads)
	repo.Client = &auth.Client{
		Client: &http.Client{
			Transport: &http.Transport{
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          10,
				MaxIdleConnsPerHost:   4,
				MaxConnsPerHost:       8,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				ExpectContinueTimeout: 5 * time.Second,
				WriteBufferSize:       256 * 1024,
				ReadBufferSize:        256 * 1024,
				TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		Cache: auth.NewCache(),
	}

	// Extract tag
	tag := "latest"
	if idx := strings.LastIndex(ref, ":"); idx >= 0 {
		tag = ref[idx+1:]
	}

	// Build the set of media types we want for this platform
	currentOS := runtime.GOOS
	currentArch := runtime.GOARCH
	binaryMediaType := fmt.Sprintf("application/vnd.sourceplane.bin.%s-%s", currentOS, currentArch)

	wantedTypes := map[string]bool{
		"application/vnd.sourceplane.provider.v1": true,
		"application/vnd.sourceplane.assets.v1":   true,
		binaryMediaType:                           true,
	}

	// Track which layers we actually download for progress display
	var mu sync.Mutex
	startTimes := map[string]time.Time{}

	// In-memory target store for the copy
	memStore := memory.New()

	// Use oras.Copy — handles manifest resolution, concurrent layer downloads,
	// deduplication, and streaming in one call
	copyOpts := oras.CopyOptions{
		CopyGraphOptions: oras.CopyGraphOptions{
			Concurrency: 4, // parallel layer downloads

			// Filter to only download platform-relevant layers
			FindSuccessors: func(ctx context.Context, fetcher content.Fetcher, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				successors, err := content.Successors(ctx, fetcher, desc)
				if err != nil {
					return nil, err
				}

				// For the manifest node, filter layers to platform-relevant ones
				if desc.MediaType == ocispec.MediaTypeImageManifest ||
					desc.MediaType == "application/vnd.oci.image.manifest.v1+json" {
					var filtered []ocispec.Descriptor
					foundBinary := false
					for _, s := range successors {
						if wantedTypes[s.MediaType] {
							filtered = append(filtered, s)
							if s.MediaType == binaryMediaType {
								foundBinary = true
							}
						} else if s.MediaType == ocispec.MediaTypeImageConfig ||
							s.MediaType == "application/vnd.oci.image.config.v1+json" {
							// Always include the config
							filtered = append(filtered, s)
						}
						// Skip other platform binaries and empty layers
					}
					if !foundBinary {
						// Fallback: include all non-empty layers for backwards compat
						fmt.Printf("⚠ No binary for %s/%s, downloading all layers...\n", currentOS, currentArch)
						filtered = nil
						for _, s := range successors {
							if s.MediaType != "application/vnd.oci.empty.v1+json" {
								filtered = append(filtered, s)
							}
						}
					}
					if len(filtered) > 0 {
						fmt.Printf("✓ Fetching %d layers (platform: %s/%s)...\n", len(filtered), currentOS, currentArch)
					}
					return filtered, nil
				}
				return successors, nil
			},

			PreCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				mu.Lock()
				startTimes[desc.Digest.String()] = time.Now()
				mu.Unlock()
				handler.OnNodeDownloading(desc)
				return nil
			},

			PostCopy: func(ctx context.Context, desc ocispec.Descriptor) error {
				handler.OnNodeDownloaded(desc)
				return nil
			},

			OnCopySkipped: func(ctx context.Context, desc ocispec.Descriptor) error {
				handler.OnNodeSkipped(desc)
				return nil
			},
		},
	}

	fmt.Printf("Pulling from %s...\n", ref)
	rootDesc, err := oras.Copy(ctx, repo, tag, memStore, tag, copyOpts)
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	fmt.Printf("✓ Pulled manifest %s\n", rootDesc.Digest.String()[:16])

	// Now extract the downloaded content from the memory store
	// Fetch the manifest to find layers
	manifestRC, err := memStore.Fetch(ctx, rootDesc)
	if err != nil {
		return fmt.Errorf("failed to read manifest from store: %w", err)
	}
	manifestData, err := io.ReadAll(manifestRC)
	manifestRC.Close()
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Extract each layer from the memory store
	for _, layer := range manifest.Layers {
		if layer.MediaType == "application/vnd.oci.empty.v1+json" {
			continue
		}
		// Only extract layers we wanted (or all if fallback)
		if !wantedTypes[layer.MediaType] && !strings.HasPrefix(layer.MediaType, "application/vnd.sourceplane.") {
			continue
		}

		exists, _ := memStore.Exists(ctx, layer)
		if !exists {
			continue // was filtered out
		}

		layerData, err := content.FetchAll(ctx, memStore, layer)
		if err != nil {
			return fmt.Errorf("failed to read layer %s: %w", layer.Digest.String()[:16], err)
		}
		if err := extractLayerContent(layerData, providerBaseDir); err != nil {
			return fmt.Errorf("failed to extract layer: %w", err)
		}
	}

	// Extract config if non-empty
	if manifest.Config.Size > 2 {
		if exists, _ := memStore.Exists(ctx, manifest.Config); exists {
			configData, err := content.FetchAll(ctx, memStore, manifest.Config)
			if err == nil {
				extractLayerContent(configData, providerBaseDir)
			}
		}
	}

	// Ensure directory structure
	for _, dir := range []string{"bin", "assets"} {
		os.MkdirAll(filepath.Join(providerBaseDir, dir), 0755)
	}

	// Relocate oci/ subdirectory if extraction created one
	ociDir := filepath.Join(providerBaseDir, "oci")
	if stat, err := os.Stat(ociDir); err == nil && stat.IsDir() {
		for _, item := range []string{"bin", "assets"} {
			src := filepath.Join(ociDir, item)
			dst := filepath.Join(providerBaseDir, item)
			if srcStat, err := os.Stat(src); err == nil && srcStat.IsDir() {
				if err := copyDir(src, dst); err == nil {
					os.RemoveAll(src)
				}
			}
		}
		os.RemoveAll(ociDir)
	}

	// Verify provider manifest
	manifestPath := filepath.Join(providerBaseDir, "thin.provider.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		fmt.Printf("⚠ Warning: provider manifest not found at %s\n", manifestPath)
	}

	// Verify and chmod binary
	binPath, err := GetPlatformBinaryPath(providerBaseDir)
	if err != nil {
		fmt.Printf("⚠ Warning: %v\n", err)
	} else {
		if err := os.Chmod(binPath, 0755); err != nil {
			return fmt.Errorf("failed to make binary executable: %w", err)
		}
		fmt.Printf("✓ Binary ready: %s\n", filepath.Base(binPath))
	}

	fmt.Printf("✓ Provider %s installed from %s\n", providerName, imageRef)
	return nil
}

// extractLayerContent extracts tar/tar.gz layer content to target directory
func extractLayerContent(layerData []byte, targetDir string) error {
	// Check if it's a gzipped tar
	if bytes.HasPrefix(layerData, []byte{0x1f, 0x8b}) {
		return extractTarGz(bytes.NewReader(layerData), targetDir)
	}

	// Check if it's plain tar
	if isTar(layerData) {
		return extractTar(bytes.NewReader(layerData), targetDir)
	}

	// Check if it's a raw binary (Mach-O, ELF, etc.) - 4.4MB+
	if len(layerData) > 4000000 {
		// Large binary file - extract directly to bin/entrypoint
		binPath := filepath.Join(targetDir, "bin", "entrypoint")
		if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(binPath, layerData, 0755)
	}

	// Check if it's YAML/config file (provider manifest, etc.)
	if len(layerData) > 0 && layerData[0] >= 32 && layerData[0] < 127 {
		// Text file - likely YAML or JSON
		// Save as thin.provider.yaml in root
		manifestPath := filepath.Join(targetDir, "thin.provider.yaml")
		return os.WriteFile(manifestPath, layerData, 0644)
	}

	// Not a recognized format, skip
	return nil
}

// extractTarGz extracts a tar.gz archive
func extractTarGz(reader io.Reader, targetDir string) error {
	// Read all content first
	content, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read gzip: %w", err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	return extractTarWithReader(gz, targetDir)
}

// extractTar extracts a tar archive
func extractTar(reader io.Reader, targetDir string) error {
	// Read all content first to allow multiple passes
	content, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read tar: %w", err)
	}

	return extractTarWithReader(bytes.NewReader(content), targetDir)
}

// extractTarWithReader extracts a tar from a reader
func extractTarWithReader(reader io.Reader, targetDir string) error {
	tr := tar.NewReader(reader)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		targetPath := filepath.Join(targetDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			file, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, tr); err != nil {
				file.Close()
				return err
			}
			file.Close()
			// Preserve execute bit
			if err := os.Chmod(targetPath, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}

	return nil
}

// isTar checks if content is tar format
func isTar(data []byte) bool {
	if len(data) < 512 {
		return false
	}
	// TAR magic is at offset 257
	return string(data[257:262]) == "ustar"
}

// GetPlatformBinaryPath returns the path to the platform-specific binary for a provider
func GetPlatformBinaryPath(providerDir string) (string, error) {
	goos := runtime.GOOS
	arch := runtime.GOARCH

	// Try new platform-specific flat structure: bin/entrypoint (from platform-specific layer)
	flatBinaryPath := filepath.Join(providerDir, "bin", "entrypoint")
	if _, err := os.Stat(flatBinaryPath); err == nil {
		return flatBinaryPath, nil
	}

	// Try alternate names in flat structure
	for _, name := range []string{"thin", "provider"} {
		altPath := filepath.Join(providerDir, "bin", name)
		if _, err := os.Stat(altPath); err == nil {
			return altPath, nil
		}
	}

	// Try old multi-platform nested structure: bin/{os}/{arch}/entrypoint
	nestedBinaryPath := filepath.Join(providerDir, "bin", goos, arch, "entrypoint")
	if _, err := os.Stat(nestedBinaryPath); err == nil {
		return nestedBinaryPath, nil
	}

	// Try alternate names in nested structure
	for _, name := range []string{"thin", "provider"} {
		altPath := filepath.Join(providerDir, "bin", goos, arch, name)
		if _, err := os.Stat(altPath); err == nil {
			return altPath, nil
		}
	}

	return "", fmt.Errorf("binary not found for platform %s/%s (checked bin/entrypoint and bin/%s/%s/entrypoint)", goos, arch, goos, arch)
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}

			// Get source file permissions
			srcInfo, err := os.Stat(srcPath)
			if err != nil {
				return err
			}

			if err := os.WriteFile(dstPath, data, srcInfo.Mode()); err != nil {
				return err
			}
		}
	}

	return nil
}
