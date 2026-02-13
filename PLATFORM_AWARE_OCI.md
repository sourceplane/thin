# Platform-Aware OCI Provider Installation

## Overview

The provider installer now supports two OCI manifest structures:

### 1. **New Platform-Specific Structure** (Recommended)
Separate OCI layers per platform binary:
```
Layers:
  - application/vnd.sourceplane.provider.v1 (4.47KB) - Provider manifest
  - application/vnd.sourceplane.assets.v1 (3.01KB) - Common assets
  - application/vnd.sourceplane.bin.linux-amd64 (~4.4MB) - Linux AMD64 binary
  - application/vnd.sourceplane.bin.linux-arm64 (~4.2MB) - Linux ARM64 binary
  - application/vnd.sourceplane.bin.darwin-amd64 (~4.5MB) - macOS AMD64 binary
  - application/vnd.sourceplane.bin.darwin-arm64 (~4.4MB) - macOS ARM64 binary
```

**Behavior**: Downloads ONLY the layers needed for the current platform
- Provider (always)
- Assets (always)
- Platform-specific binary (only current OS/arch)
- **Bandwidth savings**: 75-85% on binary downloads

### 2. **Legacy Multi-Platform Structure** (Backward Compatible)
All platform binaries in single layer(s):
```
Layers:
  - application/vnd.oras.v1 (5332B) - Provider manifest
  - application/vnd.oras.v1 (3086B) - Assets
  - application/vnd.oras.v1 (18.2MB) - All platform binaries
```

**Behavior**: Detects missing platform-specific layers, falls back to multi-platform layer download
- Downloads all layers
- Extracts platform-specific binaries during processing
- Works transparently with existing images

## Implementation

### MediaType Detection

The installer checks layer mediaTypes in order:
1. `application/vnd.sourceplane.provider.v1` - Provider manifest
2. `application/vnd.sourceplane.assets.v1` - Common assets/schemas
3. `application/vnd.sourceplane.bin.{os}-{arch}` - Platform-specific binary

### Parallel Downloading

- **2 concurrent workers** for downloading layers
- Provider and assets download in parallel
- Platform binary downloads concurrently with other layers
- Sequential fallback for unknown structures

### Runtime Platform Detection

Uses Go's `runtime.GOOS` and `runtime.GOARCH` to determine:
- Current operating system (darwin, linux, etc.)
- Current CPU architecture (amd64, arm64, etc.)

Constructs expected mediaType: `application/vnd.sourceplane.bin.{os}-{arch}`

## Example Output

### Platform-Specific Structure (New)
```
Downloading lite from ghcr.io/sourceplane/lite-ci:v0.2.0...
✓ Resolved image digest: sha256:a0544b318
Fetching manifest...
✓ Fetching 3 layers (platform: darwin/arm64)...
↓ Pulling sha256:5862ffe39 (4.47KB)
↓ Pulling sha256:7a2441f8f (3.01KB)
↓ Pulling sha256:3433ea3ed (4.4MB)
✓ Pulled sha256:7a2441f8f (0B/s)
  └─ sha256:7a2441f8faaca814f509552061d7583db76fdbc47094c722bfb265716f9380a6
✓ Pulled sha256:5862ffe39 (0B/s)
  └─ sha256:5862ffe39e26e33d896b1af224d34a4ba69b5e0735b22864457eb029e756d1d0
✓ Pulled sha256:3433ea3ed (4.4MB/s)
  └─ sha256:3433ea3ed79fcd2b5a59aa507680af860d5387a6f39f3fe417478a4c56b0c9aa
✓ Binary ready: entrypoint
✓ Provider lite installed from ghcr.io/sourceplane/lite-ci:v0.2.0
```

### Legacy Multi-Platform Structure (Fallback)
```
Downloading lite from ghcr.io/sourceplane/lite-ci:v0.2.0...
✓ Resolved image digest: sha256:a0544b318
Fetching manifest...
⚠ Platform-specific binary layer not found for darwin/arm64, checking for multi-platform layers...
✓ Fetching 3 layers (platform: darwin/arm64)...
↓ Pulling sha256:5862ffe39 (4.47KB)
↓ Pulling sha256:7a2441f8f (3.01KB)
↓ Pulling sha256:3433ea3ed (18.2MB)  [← All platforms included]
✓ Pulled sha256:7a2441f8f (0B/s)
✓ Pulled sha256:5862ffe39 (0B/s)
✓ Pulled sha256:3433ea3ed (5.75MB/s)
✓ Binary ready: entrypoint
✓ Provider lite installed from ghcr.io/sourceplane/lite-ci:v0.2.0
```

## Creating Platform-Specific Provider Images

### Dockerfile Example

```dockerfile
FROM scratch as builder

# Layer 1: Provider manifest
COPY thin.provider.yaml /
LABEL org.opencontainers.image.layers.provider.v1=true

# Layer 2: Common assets
COPY assets /assets
LABEL org.opencontainers.image.layers.assets.v1=true

# Layer 3: Linux AMD64 binary
FROM builder as linux-amd64
COPY bin/linux/amd64/entrypoint /entrypoint
LABEL org.opencontainers.image.platforms="linux/amd64"

# Layer 4: Linux ARM64 binary
FROM builder as linux-arm64
COPY bin/linux/arm64/entrypoint /entrypoint
LABEL org.opencontainers.image.platforms="linux/arm64"

# Layer 5: Darwin AMD64 binary
FROM builder as darwin-amd64
COPY bin/darwin/amd64/entrypoint /entrypoint
LABEL org.opencontainers.image.platforms="darwin/amd64"

# Layer 6: Darwin ARM64 binary
FROM builder as darwin-arm64
COPY bin/darwin/arm64/entrypoint /entrypoint
LABEL org.opencontainers.image.platforms="darwin/arm64"

# Multi-platform manifest
FROM scratch
COPY --from=provider /thin.provider.yaml /
COPY --from=assets /assets /assets
COPY --from=linux-amd64 /entrypoint /bin/linux/amd64/entrypoint
COPY --from=linux-arm64 /entrypoint /bin/linux/arm64/entrypoint
COPY --from=darwin-amd64 /entrypoint /bin/darwin/amd64/entrypoint
COPY --from=darwin-arm64 /entrypoint /bin/darwin/arm64/entrypoint
```

### Using ORAS CLI to Create Layers

```bash
# Create config
echo '{}' > config.json

# Add provider manifest
oras push ghcr.io/sourceplane/provider:latest \
  --config config.json \
  thin.provider.yaml:application/vnd.sourceplane.provider.v1

# Add assets
oras push ghcr.io/sourceplane/provider:latest \
  assets:application/vnd.sourceplane.assets.v1

# Add platform-specific binaries
oras push ghcr.io/sourceplane/provider:latest \
  --image-spec v1_1_0 \
  bin/linux/amd64/entrypoint:application/vnd.sourceplane.bin.linux-amd64 \
  bin/linux/arm64/entrypoint:application/vnd.sourceplane.bin.linux-arm64 \
  bin/darwin/amd64/entrypoint:application/vnd.sourceplane.bin.darwin-amd64 \
  bin/darwin/arm64/entrypoint:application/vnd.sourceplane.bin.darwin-arm64
```

## Performance Benefits

| Metric | Multi-Platform Layer | Platform-Specific Layers |
|--------|----------------------|--------------------------|
| Install on darwin/arm64 | 18.2 MB | 4.4 MB |
| Bandwidth Saved | - | **75.8%** |
| Container Cache Hit | No | Yes (per platform) |
| OCI Best Practice | No | Yes |

## Backward Compatibility

✅ Existing multi-platform layer images continue to work
✅ No breaking changes to provider format
✅ Automatic fallback detection
✅ Same final output structure

## Code Changes

**File**: `internal/runtime/oci.go`

- **Layer filtering** by mediaType
- **Platform detection** using `runtime.GOOS` and `runtime.GOARCH`
- **Fallback mechanism** for legacy structure
- **Parallel downloading** with 2 concurrent workers
- **Bandwidth optimization** via selective layer download
