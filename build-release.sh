#!/bin/bash
set -e

echo "Building vfio-usb-passthrough release..."

# Check if pnpm is available
if ! command -v pnpm &> /dev/null; then
    echo "Error: pnpm is not installed. Please install pnpm first."
    exit 1
fi

# Check if go is available
if ! command -v go &> /dev/null; then
    echo "Error: go is not installed. Please install Go first."
    exit 1
fi

# Install pnpm dependencies if needed
if [ ! -d "node_modules" ]; then
    echo "Installing pnpm dependencies..."
    pnpm install
fi

# Build frontend assets
echo "Building frontend assets..."
pnpm run build

# Verify that assets/dist exists and has files
if [ ! -d "assets/dist" ] || [ -z "$(ls -A assets/dist)" ]; then
    echo "Error: assets/dist directory is empty or does not exist after build"
    exit 1
fi

# Build Go binary with embedded files
echo "Building Go binary..."
go build -o vfio-usb-passthrough -ldflags="-s -w" .

# Make binary executable
chmod +x vfio-usb-passthrough

echo "Build complete! Binary: ./vfio-usb-passthrough"

