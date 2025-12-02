package utils

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
)

// USBDevice represents a USB device with vendor and product IDs
type USBDevice struct {
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
	Description string `json:"description,omitempty"`
}

// USBHostdevXML represents the libvirt USB hostdev XML structure
type USBHostdevXML struct {
	XMLName xml.Name `xml:"hostdev"`
	Mode    string   `xml:"mode,attr"`
	Type    string   `xml:"type,attr"`
	Source  struct {
		Vendor  struct {
			ID string `xml:"id,attr"`
		} `xml:"vendor"`
		Product struct {
			ID string `xml:"id,attr"`
		} `xml:"product"`
	} `xml:"source"`
}

// VMXML represents the structure of a VM XML dump from libvirt
type VMXML struct {
	XMLName xml.Name `xml:"domain"`
	Devices struct {
		Hostdevs []USBHostdevXML `xml:"hostdev"`
	} `xml:"devices"`
}

// GenerateUSBXML generates libvirt USB hostdev XML from vendor and product IDs
func GenerateUSBXML(vendorID, productID string) (string, error) {
	// Validate hex format
	if !isValidHexID(vendorID) || !isValidHexID(productID) {
		return "", fmt.Errorf("invalid vendor or product ID format")
	}

	// Ensure IDs are in lowercase and prefixed with 0x
	vendorID = normalizeHexID(vendorID)
	productID = normalizeHexID(productID)

	hostdev := USBHostdevXML{
		Mode: "subsystem",
		Type: "usb",
	}
	hostdev.Source.Vendor.ID = vendorID
	hostdev.Source.Product.ID = productID

	output, err := xml.MarshalIndent(&hostdev, "", "    ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal XML: %w", err)
	}

	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + string(output), nil
}

// ParseVMXML extracts attached USB devices from VM XML dump
func ParseVMXML(vmXML string) ([]USBDevice, error) {
	var vm VMXML
	var devices []USBDevice

	// Unmarshal the XML into our struct
	err := xml.Unmarshal([]byte(vmXML), &vm)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VM XML: %w", err)
	}

	// Iterate through all hostdev entries and filter for USB devices
	for _, hostdev := range vm.Devices.Hostdevs {
		// Only process USB hostdev entries with subsystem mode
		if hostdev.Mode == "subsystem" && hostdev.Type == "usb" {
			// Extract vendor and product IDs
			vendorID := hostdev.Source.Vendor.ID
			productID := hostdev.Source.Product.ID

			// Skip if vendor or product ID is missing
			if vendorID == "" || productID == "" {
				continue
			}

			// Normalize IDs: remove 0x prefix and convert to lowercase
			vendorID = strings.ToLower(strings.TrimPrefix(vendorID, "0x"))
			productID = strings.ToLower(strings.TrimPrefix(productID, "0x"))

			// Validate the IDs are 4-digit hex values
			if !isValidHexID(vendorID) || !isValidHexID(productID) {
				continue
			}

			device := USBDevice{
				VendorID:  vendorID,
				ProductID: productID,
			}
			devices = append(devices, device)
		}
	}

	return devices, nil
}

// isValidHexID checks if a string is a valid hexadecimal ID (with or without 0x prefix)
func isValidHexID(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.TrimPrefix(id, "0x")
	matched, _ := regexp.MatchString(`^[0-9a-f]{4}$`, id)
	return matched
}

// normalizeHexID ensures the hex ID is in the format 0xXXXX
func normalizeHexID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.TrimPrefix(id, "0x")
	return "0x" + id
}

