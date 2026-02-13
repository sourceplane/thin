package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// PullProviderOCI pulls a provider from an OCI registry and extracts platform-specific files
func PullProviderOCI(ctx context.Context, imageRef string, providerName string) error {
	// Create provider root directory using provider name
	// The structure will be: ~/.thin/providers/<name>/
	providerBaseDir := filepath.Join(ThinHome(), "providers", providerName)

	if err := os.MkdirAll(providerBaseDir, 0755); err != nil {
		return fmt.Errorf("failed to create provider directory: %w", err)
	}

	// Create status handler for real-time progress display (ORAS CLI style)
	handler := NewStatusHandler()
	defer handler.Close()

	fmt.Printf("Downloading %s from %s...\n", providerName, imageRef)

	// Normalize the image reference if needed
	ref := imageRef
	if !contains(ref, "/") {
		// Add default registry
		ref = "docker.io/" + ref
	}
	if !contains(ref, ":") {
		// Add default tag
		ref = ref + ":latest"
	}

	fmt.Printf("Using reference: %s\n", ref)

	// Connect to registry with proper HTTP client
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return fmt.Errorf("failed to parse image reference %s: %w", ref, err)
	}

	// Set up HTTP client with proper user agent
	httpClient := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: false,
		},
	}

	// Set up auth client for public registries
	repo.Client = &auth.Client{
		Client: httpClient,
		Cache:  auth.NewCache(),
	}

	fmt.Printf("Connecting to registry...\n")

	// Extract just the tag/digest from ref for Resolve()
	// ref format is "registry/repo:tag" but Resolve needs just the tag
	tag := "latest"
	if idx := lastIndexOf(ref, ":"); idx >= 0 {
		tag = ref[idx+1:]
	}

	// Resolve the reference - must pass the tag or digest, not the full ref
	desc, err := repo.Resolve(ctx, tag)
	if err != nil {
		return fmt.Errorf("failed to resolve image %s with tag %s: %w\nTip: Make sure the image is public or provide credentials", imageRef, tag, err)
	}

	fmt.Printf("✓ Resolved image digest: %s\n", desc.Digest.String()[:16])

	// Fetch the manifest
	fmt.Printf("Fetching manifest...\n")
	manifestReader, err := repo.Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest: %w", err)
	}
	defer manifestReader.Close()

	manifestData, err := io.ReadAll(manifestReader)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Parse manifest to get layers
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Categorize layers by mediaType
	currentOS := runtime.GOOS
	currentArch := runtime.GOARCH
	
	var providerLayer *ocispec.Descriptor
	var assetsLayer *ocispec.Descriptor
	var binaryLayer *ocispec.Descriptor
	var layersToDownload []*ocispec.Descriptor

	// Match binary mediaType for current platform: application/vnd.sourceplane.bin.{os}-{arch}
	binaryMediaType := fmt.Sprintf("application/vnd.sourceplane.bin.%s-%s", currentOS, currentArch)

	for i, layer := range manifest.Layers {
		switch {
		case layer.MediaType == "application/vnd.oci.empty.v1+json":
			// Skip empty config layers
			continue
		case layer.MediaType == "application/vnd.sourceplane.provider.v1":
			providerLayer = &manifest.Layers[i]
		case layer.MediaType == "application/vnd.sourceplane.assets.v1":
			assetsLayer = &manifest.Layers[i]
		case layer.MediaType == binaryMediaType:
			binaryLayer = &manifest.Layers[i]
		case strings.HasPrefix(layer.MediaType, "application/vnd.sourceplane.bin."):
			// Skip other platform binaries (not for current platform)
			continue
		default:
			// Other layers (examples, etc.) - optional, skip for now
			continue
		}
	}

	// Build layers to download
	if providerLayer != nil {
		layersToDownload = append(layersToDownload, providerLayer)
	}
	if assetsLayer != nil {
		layersToDownload = append(layersToDownload, assetsLayer)
	}
	if binaryLayer != nil {
		layersToDownload = append(layersToDownload, binaryLayer)
	}

	// Check if we found required layers
	if len(layersToDownload) == 0 {
		return fmt.Errorf("no compatible provider layers found in manifest")
	}
	if providerLayer == nil && assetsLayer == nil {
		// Provider data (manifest + assets) is required
		return fmt.Errorf("provider manifest layer not found in manifest")
	}
	if binaryLayer == nil {
		// Try with old structure for backwards compatibility
		fmt.Printf("⚠ Platform-specific binary layer not found for %s/%s, checking for multi-platform layers...\n", currentOS, currentArch)
		// Fall back to downloading all layers and filtering during extraction
		layersToDownload = nil
		for i := range manifest.Layers {
			if manifest.Layers[i].MediaType != "application/vnd.oci.empty.v1+json" {
				layersToDownload = append(layersToDownload, &manifest.Layers[i])
			}
		}
	}

	fmt.Printf("✓ Fetching %d layers (platform: %s/%s)...\n", len(layersToDownload), currentOS, currentArch)

	// Download layer with concurrent workers
	jobs := make(chan ocispec.Descriptor, len(layersToDownload))
	results := make(chan layerJob, len(layersToDownload))

	// Start 2 worker goroutines for parallel downloading
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for layer := range jobs {
				handler.OnNodeDownloading(layer)

				// Use tracked fetch to update progress
				layerData, err := trackedFetchAll(ctx, repo, layer, handler)
				if err != nil {
					results <- layerJob{layer: layer, data: nil, err: err}
					return
				}

				handler.OnNodeDownloaded(layer)
				results <- layerJob{layer: layer, data: layerData, err: nil}
			}
		}()
	}

	// Send layers to workers
	go func() {
		for _, layer := range layersToDownload {
			jobs <- *layer
		}
		close(jobs)
	}()

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Track downloaded layers
	downloadsCompleted := 0

	// Process results and extract
	for result := range results {
		if result.err != nil {
			return fmt.Errorf("failed to fetch layer %s: %w", result.layer.Digest.String()[:16], result.err)
		}

		handler.OnNodeProcessing(result.layer)

		// Extract based on layer type
		if err := extractLayerContent(result.data, providerBaseDir); err != nil {
			return fmt.Errorf("failed to extract layer: %w", err)
		}

		handler.OnNodeRestored(result.layer)
		downloadsCompleted++
	}

	// Also handle config blob if present and non-empty
	if manifest.Config.Size > 2 {
		fmt.Printf("✓ Processing config...\n")
		configData, err := content.FetchAll(ctx, repo, manifest.Config)
		if err == nil {
			extractLayerContent(configData, providerBaseDir)
		}
	}

	// Create necessary subdirectories if they don't exist
	for _, dir := range []string{"bin", "assets"} {
		if err := os.MkdirAll(filepath.Join(providerBaseDir, dir), 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// If extraction created an oci subdirectory, move contents to root
	ociDir := filepath.Join(providerBaseDir, "oci")
	if stat, err := os.Stat(ociDir); err == nil && stat.IsDir() {
		// Move bin and assets from oci to root
		for _, item := range []string{"bin", "assets"} {
			src := filepath.Join(ociDir, item)
			dst := filepath.Join(providerBaseDir, item)

			// Copy contents from src to dst
			if srcStat, err := os.Stat(src); err == nil {
				if srcStat.IsDir() {
					if err := copyDir(src, dst); err == nil {
						os.RemoveAll(src)
					}
				}
			}
		}
		// Remove empty oci directory
		os.RemoveAll(ociDir)
	}

	// Verify manifest exists
	manifestPath := filepath.Join(providerBaseDir, "thin.provider.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		fmt.Printf("⚠ Warning: provider manifest not found at %s\n", manifestPath)
		// Don't fail - optional metadata
	}

	// Verify binary for current platform exists
	binPath, err := GetPlatformBinaryPath(providerBaseDir)
	if err != nil {
		fmt.Printf("⚠ Warning: %v\n", err)
		// Don't fail - optional
	} else {
		// Make binary executable
		if err := os.Chmod(binPath, 0755); err != nil {
			return fmt.Errorf("failed to make binary executable: %w", err)
		}
		fmt.Printf("✓ Binary ready: %s\n", filepath.Base(binPath))
	}

	fmt.Printf("✓ Provider %s installed from %s\n", providerName, imageRef)
	return nil
}

type progressTracker struct {
	reader     io.Reader
	handler    StatusHandler
	digest     string
	bytesRead  int64
	lastUpdate time.Time
	updateFreq time.Duration
}

type layerJob struct {
	layer ocispec.Descriptor
	data  []byte
	err   error
}

func (pt *progressTracker) Read(p []byte) (int, error) {
	n, err := pt.reader.Read(p)
	if n > 0 {
		pt.bytesRead += int64(n)

		// Update handler periodically
		now := time.Now()
		if now.Sub(pt.lastUpdate) >= pt.updateFreq {
			pt.handler.UpdateProgress(pt.digest, pt.bytesRead)
			pt.lastUpdate = now
		}
	}
	return n, err
}

// trackedFetchAll fetches layer content with progress tracking
func trackedFetchAll(ctx context.Context, repo *remote.Repository, layer ocispec.Descriptor, handler StatusHandler) ([]byte, error) {
	// Use content.FetchAll for the actual fetch
	data, err := content.FetchAll(ctx, repo, layer)
	if err != nil {
		return nil, err
	}
	return data, nil
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

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// lastIndexOf finds the last occurrence of a substring
func lastIndexOf(s, substr string) int {
	last := -1
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			last = i
		}
	}
	return last
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
