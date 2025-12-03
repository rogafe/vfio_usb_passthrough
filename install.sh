#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Installing vfio-usb-passthrough service...${NC}"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Error: This script must be run as root${NC}"
    echo "Usage: sudo ./install.sh"
    exit 1
fi

# Check if binary exists
if [ ! -f "./vfio-usb-passthrough" ]; then
    echo -e "${YELLOW}Binary not found. Building it now...${NC}"
    ./build-release.sh
fi

# Create directories
echo "Creating directories..."
mkdir -p /usr/local/bin
mkdir -p /usr/local/lib/vfio-usb-passthrough
mkdir -p /var/lib/vfio-usb-passthrough

# Copy binary
echo "Installing binary..."
cp ./vfio-usb-passthrough /usr/local/bin/vfio-usb-passthrough
chmod +x /usr/local/bin/vfio-usb-passthrough

# Set up data directory
echo "Setting up data directory..."
chown root:root /var/lib/vfio-usb-passthrough
chmod 755 /var/lib/vfio-usb-passthrough

# Create symlink for data directory in working directory
# This allows the app to use ./data relative path
if [ ! -e "/usr/local/lib/vfio-usb-passthrough/data" ]; then
    ln -s /var/lib/vfio-usb-passthrough /usr/local/lib/vfio-usb-passthrough/data
fi

# Install systemd service
echo "Installing systemd service..."
cp ./vfio-usb-passthrough.service /etc/systemd/system/vfio-usb-passthrough.service
chmod 644 /etc/systemd/system/vfio-usb-passthrough.service

# Reload systemd
echo "Reloading systemd daemon..."
systemctl daemon-reload

echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "To start the service:"
echo "  sudo systemctl start vfio-usb-passthrough"
echo ""
echo "To enable the service to start on boot:"
echo "  sudo systemctl enable vfio-usb-passthrough"
echo ""
echo "To check service status:"
echo "  sudo systemctl status vfio-usb-passthrough"
echo ""
echo "To view logs:"
echo "  sudo journalctl -u vfio-usb-passthrough -f"

