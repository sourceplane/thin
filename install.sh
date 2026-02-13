#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

# Normalize architecture names
case "$ARCH" in
  x86_64) ARCH="x86_64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) 
    echo -e "${RED}✗ Unsupported architecture: $ARCH${NC}"
    exit 1
    ;;
esac

# Normalize OS names
case "$OS" in
  darwin) OS="darwin" ;;
  linux) OS="linux" ;;
  msys|mingw|windows) OS="windows" ;;
  *)
    echo -e "${RED}✗ Unsupported OS: $OS${NC}"
    exit 1
    ;;
esac

# Determine file extension
if [ "$OS" = "windows" ]; then
  EXT="zip"
else
  EXT="tar.gz"
fi

# Get the latest release version
REPO="sourceplane/thin"
echo -e "${YELLOW}→ Fetching latest release for ${OS}/${ARCH}${NC}"

LATEST_RELEASE=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep tag_name | cut -d'"' -f4)

if [ -z "$LATEST_RELEASE" ]; then
  echo -e "${RED}✗ Could not determine latest release${NC}"
  exit 1
fi

DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_RELEASE}/thin_${OS}_${ARCH}.${EXT}"

echo -e "${YELLOW}→ Version: ${LATEST_RELEASE}${NC}"
echo -e "${YELLOW}→ Downloading: ${DOWNLOAD_URL}${NC}"

# Create temporary directory
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

cd "$TEMP_DIR"

# Download
if ! curl -fL -o "thin.${EXT}" "$DOWNLOAD_URL"; then
  echo -e "${RED}✗ Download failed${NC}"
  exit 1
fi

# Extract
if [ "$EXT" = "tar.gz" ]; then
  tar -xzf "thin.${EXT}"
else
  unzip -q "thin.${EXT}"
fi

if [ ! -f "thin" ]; then
  echo -e "${RED}✗ Extraction failed - thin binary not found${NC}"
  exit 1
fi

# Make executable
chmod +x thin

# Install to /usr/local/bin
if [ -w /usr/local/bin ]; then
  mv thin /usr/local/bin/thin
  echo -e "${GREEN}✓ Installed to /usr/local/bin/thin${NC}"
else
  echo -e "${YELLOW}→ Requires sudo to install to /usr/local/bin${NC}"
  sudo mv thin /usr/local/bin/thin
  sudo chmod +x /usr/local/bin/thin
  echo -e "${GREEN}✓ Installed to /usr/local/bin/thin${NC}"
fi

# Verify installation
if command -v thin &> /dev/null; then
  VERSION=$(thin --version 2>/dev/null || echo "unknown")
  echo -e "${GREEN}✓ Installation complete!${NC}"
  echo -e "${GREEN}  thin is ready to use${NC}"
else
  echo -e "${RED}✗ Installation verification failed${NC}"
  exit 1
fi
