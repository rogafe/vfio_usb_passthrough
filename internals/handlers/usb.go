package handlers

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"vfio_usb_passthrough/internals/db"
	"vfio_usb_passthrough/internals/utils"

	"github.com/gofiber/fiber/v2"
)

// VM name validation errors
var (
	ErrVMNameEmpty         = errors.New("VM name is required")
	ErrVMNameInvalidFormat = errors.New("VM name contains invalid characters (only alphanumeric, dash, underscore allowed, max 64 chars)")
	ErrVMNotRunning        = errors.New("VM is not running or does not exist")
)

// vmNamePattern validates VM names: alphanumeric, dash, underscore only, max 64 chars
var vmNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isValidVMNameFormat checks if a VM name has valid format
func isValidVMNameFormat(vmName string) bool {
	if vmName == "" || len(vmName) > 64 {
		return false
	}
	return vmNamePattern.MatchString(vmName)
}

// getRunningVMNames returns a list of currently running VM names
func getRunningVMNames() ([]string, error) {
	cmd := exec.Command("virsh", "list", "--name", "--state-running")
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list running VMs: %w", err)
	}

	var vms []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		vmName := strings.TrimSpace(scanner.Text())
		if vmName != "" {
			vms = append(vms, vmName)
		}
	}

	return vms, nil
}

// isVMRunning checks if a VM is currently running
func isVMRunning(vmName string) bool {
	runningVMs, err := getRunningVMNames()
	if err != nil {
		log.Printf("Error checking running VMs: %v", err)
		return false
	}

	for _, vm := range runningVMs {
		if vm == vmName {
			return true
		}
	}
	return false
}

// validateVMName performs full validation of a VM name
func validateVMName(vmName string) error {
	if vmName == "" {
		return ErrVMNameEmpty
	}

	if !isValidVMNameFormat(vmName) {
		return ErrVMNameInvalidFormat
	}

	if !isVMRunning(vmName) {
		return ErrVMNotRunning
	}

	return nil
}

// VMResponse represents a VM in the API response
type VMResponse struct {
	Name string `json:"name"`
}

// USBDeviceResponse represents a USB device in the API response
type USBDeviceResponse struct {
	VendorID    string `json:"vendorId"`
	ProductID   string `json:"productId"`
	Description string `json:"description"`
}

// AttachedDeviceResponse represents an attached device for a VM
type AttachedDeviceResponse struct {
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
}

// FavoriteDeviceResponse represents a favorite device in the API response
type FavoriteDeviceResponse struct {
	VendorID    string `json:"vendorId"`
	ProductID   string `json:"productId"`
	Description string `json:"description"`
}

// AttachDetachRequest represents a request to attach/detach a device
type AttachDetachRequest struct {
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
}

// DevicesStateResponse represents the combined state of all devices
type DevicesStateResponse struct {
	Devices         []USBDeviceResponse      `json:"devices"`
	AttachedDevices []AttachedDeviceResponse `json:"attachedDevices"`
	Favorites       []FavoriteDeviceResponse `json:"favorites"`
}

// ListRunningVMs returns a list of running VMs
func ListRunningVMs(c *fiber.Ctx) error {
	cmd := exec.Command("virsh", "list", "--name", "--state-running")
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")

	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error listing VMs: %v", err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to list running VMs",
			"details": err.Error(),
		})
	}

	var vms []VMResponse
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		vmName := strings.TrimSpace(scanner.Text())
		if vmName != "" {
			vms = append(vms, VMResponse{Name: vmName})
		}
	}

	return c.JSON(fiber.Map{
		"vms": vms,
	})
}

// ListUSBDevices returns a list of available USB devices
func ListUSBDevices(c *fiber.Ctx) error {
	devices, err := getUSBDevicesList()
	if err != nil {
		log.Printf("Error listing USB devices: %v", err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to list USB devices",
			"details": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"devices": devices,
	})
}

// GetAttachedDevices returns a list of USB devices attached to a VM
func GetAttachedDevices(c *fiber.Ctx) error {
	vmName := c.Params("vmName")

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		log.Printf("GetAttachedDevices: VM validation failed for '%s': %v", vmName, err)
		return c.Status(400).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	devices, err := getAttachedDevicesList(vmName)
	if err != nil {
		log.Printf("Error getting attached devices for %s: %v", vmName, err)
		return c.Status(500).JSON(fiber.Map{
			"error":   fmt.Sprintf("Failed to get attached devices for %s", vmName),
			"details": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"devices": devices,
	})
}

// GetDevicesState returns a combined state of all USB devices, attached devices, and favorites
// This endpoint eliminates multiple round-trips and race conditions
func GetDevicesState(c *fiber.Ctx) error {
	vmName := c.Query("vmName", "")

	// Validate VM name if provided
	if vmName != "" {
		if err := validateVMName(vmName); err != nil {
			log.Printf("GetDevicesState: VM validation failed for '%s': %v", vmName, err)
			return c.Status(400).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
	}

	// Run independent operations in parallel using goroutines
	var usbDevices []USBDeviceResponse
	var attachedDevices []AttachedDeviceResponse
	var favorites []db.FavoriteDevice

	var wg sync.WaitGroup
	var usbErr, attachedErr, favoritesErr error

	// Get USB devices
	wg.Add(1)
	go func() {
		defer wg.Done()
		usbDevices, usbErr = getUSBDevicesList()
	}()

	// Get attached devices if VM is selected
	if vmName != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attachedDevices, attachedErr = getAttachedDevicesList(vmName)
		}()
	}

	// Get favorites
	wg.Add(1)
	go func() {
		defer wg.Done()
		favorites, favoritesErr = db.GetAllFavorites()
	}()

	// Wait for all goroutines to complete
	wg.Wait()

	// Handle errors
	if usbErr != nil {
		log.Printf("Error getting USB devices: %v", usbErr)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to list USB devices",
			"details": usbErr.Error(),
		})
	}

	if attachedErr != nil {
		// Log error but continue - attached devices might not be available
		log.Printf("Warning: Failed to get attached devices for %s: %v", vmName, attachedErr)
		attachedDevices = []AttachedDeviceResponse{}
	}

	if favoritesErr != nil {
		log.Printf("Warning: Failed to get favorites: %v", favoritesErr)
		favorites = []db.FavoriteDevice{}
	}

	// Convert favorites to response format
	var favoritesResponse []FavoriteDeviceResponse
	for _, fav := range favorites {
		favoritesResponse = append(favoritesResponse, FavoriteDeviceResponse{
			VendorID:    fav.VendorID,
			ProductID:   fav.ProductID,
			Description: fav.Description,
		})
	}

	// Ensure we return empty arrays instead of null
	if usbDevices == nil {
		usbDevices = []USBDeviceResponse{}
	}
	if attachedDevices == nil {
		attachedDevices = []AttachedDeviceResponse{}
	}
	if favoritesResponse == nil {
		favoritesResponse = []FavoriteDeviceResponse{}
	}

	return c.JSON(DevicesStateResponse{
		Devices:         usbDevices,
		AttachedDevices: attachedDevices,
		Favorites:       favoritesResponse,
	})
}

// AttachDevice attaches a USB device to a VM
func AttachDevice(c *fiber.Ctx) error {
	vmName := c.Params("vmName")

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		log.Printf("AttachDevice: VM validation failed for '%s': %v", vmName, err)
		return c.Status(400).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var req AttachDetachRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
	}

	if req.VendorID == "" || req.ProductID == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "vendorId and productId are required",
		})
	}

	// Normalize vendor and product IDs to ensure consistent format (lowercase, no 0x prefix)
	vendorID := strings.ToLower(strings.TrimSpace(req.VendorID))
	productID := strings.ToLower(strings.TrimSpace(req.ProductID))
	vendorID = strings.TrimPrefix(vendorID, "0x")
	productID = strings.TrimPrefix(productID, "0x")

	log.Printf("AttachDevice: VM=%s, VendorID=%s, ProductID=%s (normalized from %s:%s)",
		vmName, vendorID, productID, req.VendorID, req.ProductID)

	// Generate XML
	xml, err := utils.GenerateUSBXML(vendorID, productID)
	if err != nil {
		log.Printf("Error generating XML for device %s:%s: %v", vendorID, productID, err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to generate device XML",
			"details": err.Error(),
		})
	}

	log.Printf("Generated XML for attach: %s", xml)

	// Create a temporary file for the XML
	tmpFile, err := createTempXMLFile(xml)
	if err != nil {
		log.Printf("Error creating temp XML file: %v", err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to create temporary XML file",
			"details": err.Error(),
		})
	}
	defer removeTempFile(tmpFile)

	// Execute virsh attach-device
	cmd := exec.Command("virsh", "attach-device", vmName, tmpFile, "--live")
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error attaching device to %s: %v, output: %s", vmName, err, string(output))
		return c.Status(500).JSON(fiber.Map{
			"error":   fmt.Sprintf("Failed to attach device to %s", vmName),
			"details": string(output),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": fmt.Sprintf("Device %s:%s attached to %s", vendorID, productID, vmName),
	})
}

// DetachDevice detaches a USB device from a VM
func DetachDevice(c *fiber.Ctx) error {
	vmName := c.Params("vmName")

	// Validate VM name
	if err := validateVMName(vmName); err != nil {
		log.Printf("DetachDevice: VM validation failed for '%s': %v", vmName, err)
		return c.Status(400).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var req AttachDetachRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
	}

	if req.VendorID == "" || req.ProductID == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "vendorId and productId are required",
		})
	}

	// Normalize vendor and product IDs to ensure consistent format (lowercase, no 0x prefix)
	vendorID := strings.ToLower(strings.TrimSpace(req.VendorID))
	productID := strings.ToLower(strings.TrimSpace(req.ProductID))
	vendorID = strings.TrimPrefix(vendorID, "0x")
	productID = strings.TrimPrefix(productID, "0x")

	log.Printf("DetachDevice: VM=%s, VendorID=%s, ProductID=%s (normalized from %s:%s)",
		vmName, vendorID, productID, req.VendorID, req.ProductID)

	// Generate XML
	xml, err := utils.GenerateUSBXML(vendorID, productID)
	if err != nil {
		log.Printf("Error generating XML for device %s:%s: %v", vendorID, productID, err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to generate device XML",
			"details": err.Error(),
		})
	}

	log.Printf("Generated XML for detach: %s", xml)

	// Create a temporary file for the XML
	tmpFile, err := createTempXMLFile(xml)
	if err != nil {
		log.Printf("Error creating temp XML file: %v", err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to create temporary XML file",
			"details": err.Error(),
		})
	}
	defer removeTempFile(tmpFile)

	// Execute virsh detach-device
	cmd := exec.Command("virsh", "detach-device", vmName, tmpFile, "--live")
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error detaching device from %s: %v, output: %s", vmName, err, string(output))
		return c.Status(500).JSON(fiber.Map{
			"error":   fmt.Sprintf("Failed to detach device from %s", vmName),
			"details": string(output),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": fmt.Sprintf("Device %s:%s detached from %s", vendorID, productID, vmName),
	})
}

// Helper functions for temporary file management
func createTempXMLFile(content string) (string, error) {
	tmpFile, err := os.CreateTemp("", "vfio-usb-*.xml")
	if err != nil {
		return "", err
	}
	filePath := tmpFile.Name()

	_, err = tmpFile.WriteString(content)
	if err != nil {
		tmpFile.Close()
		os.Remove(filePath)
		return "", err
	}

	err = tmpFile.Close()
	if err != nil {
		os.Remove(filePath)
		return "", err
	}

	return filePath, nil
}

func removeTempFile(filePath string) {
	os.Remove(filePath)
}

// Helper functions to get data
func getUSBDevicesList() ([]USBDeviceResponse, error) {
	cmd := exec.Command("lsusb")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var devices []USBDeviceResponse
	linePattern := regexp.MustCompile(`ID\s+([0-9a-fA-F]{4}):([0-9a-fA-F]{4})\s+(.+)`)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		matches := linePattern.FindStringSubmatch(line)
		if len(matches) >= 4 {
			devices = append(devices, USBDeviceResponse{
				VendorID:    strings.ToLower(matches[1]),
				ProductID:   strings.ToLower(matches[2]),
				Description: strings.TrimSpace(matches[3]),
			})
		}
	}
	return devices, nil
}

func getAttachedDevicesList(vmName string) ([]AttachedDeviceResponse, error) {
	cmd := exec.Command("virsh", "dumpxml", vmName)
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	attachedDevices, err := utils.ParseVMXML(string(output))
	if err != nil {
		return nil, err
	}

	var devices []AttachedDeviceResponse
	for _, device := range attachedDevices {
		devices = append(devices, AttachedDeviceResponse{
			VendorID:  device.VendorID,
			ProductID: device.ProductID,
		})
	}
	return devices, nil
}
