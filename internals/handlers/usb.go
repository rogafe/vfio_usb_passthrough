package handlers

import (
	"bufio"
	"encoding/json"
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

// AttachDetachRequest represents a request to attach/detach a device
type AttachDetachRequest struct {
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
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
	cmd := exec.Command("lsusb")

	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error listing USB devices: %v", err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to list USB devices",
			"details": err.Error(),
		})
	}

	var devices []USBDeviceResponse
	// Pattern: Bus 001 Device 002: ID 046d:c548 Logitech, Inc.
	// We want to extract: 046d:c548 and the description
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

	return c.JSON(fiber.Map{
		"devices": devices,
	})
}

// GetAttachedDevices returns a list of USB devices attached to a VM
func GetAttachedDevices(c *fiber.Ctx) error {
	vmName := c.Params("vmName")
	if vmName == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "VM name is required",
		})
	}

	cmd := exec.Command("virsh", "dumpxml", vmName)
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")

	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error getting VM XML for %s: %v", vmName, err)
		return c.Status(500).JSON(fiber.Map{
			"error":   fmt.Sprintf("Failed to get VM information for %s", vmName),
			"details": err.Error(),
		})
	}

	attachedDevices, err := utils.ParseVMXML(string(output))
	if err != nil {
		log.Printf("Error parsing VM XML for %s: %v", vmName, err)
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to parse VM XML",
			"details": err.Error(),
		})
	}

	var devices []AttachedDeviceResponse
	for _, device := range attachedDevices {
		devices = append(devices, AttachedDeviceResponse{
			VendorID:  device.VendorID,
			ProductID: device.ProductID,
		})
	}

	return c.JSON(fiber.Map{
		"devices": devices,
	})
}

// AttachDevice attaches a USB device to a VM
func AttachDevice(c *fiber.Ctx) error {
	vmName := c.Params("vmName")
	if vmName == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "VM name is required",
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
		// Check if this is an HTMX request
		if c.Get("HX-Request") == "true" {
			return c.Status(500).SendString(fmt.Sprintf("<tr><td colspan='4' class='text-center text-error'>Failed to attach device: %s</td></tr>", string(output)))
		}
		return c.Status(500).JSON(fiber.Map{
			"error":   fmt.Sprintf("Failed to attach device to %s", vmName),
			"details": string(output),
		})
	}

	// If HTMX request, return the updated table fragment
	if c.Get("HX-Request") == "true" {
		// Return device table fragment and trigger favorites refresh
		c.Set("HX-Trigger", "refreshFavorites, refreshTable")
		return GetDeviceTableFragment(c)
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": fmt.Sprintf("Device %s:%s attached to %s", vendorID, productID, vmName),
	})
}

// DetachDevice detaches a USB device from a VM
func DetachDevice(c *fiber.Ctx) error {
	vmName := c.Params("vmName")
	if vmName == "" {
		return c.Status(400).JSON(fiber.Map{
			"error": "VM name is required",
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
		// Check if this is an HTMX request
		if c.Get("HX-Request") == "true" {
			return c.Status(500).SendString(fmt.Sprintf("<tr><td colspan='4' class='text-center text-error'>Failed to detach device: %s</td></tr>", string(output)))
		}
		return c.Status(500).JSON(fiber.Map{
			"error":   fmt.Sprintf("Failed to detach device from %s", vmName),
			"details": string(output),
		})
	}

	// If HTMX request, return the updated table fragment
	if c.Get("HX-Request") == "true" {
		// Return device table fragment and trigger favorites refresh
		c.Set("HX-Trigger", "refreshFavorites, refreshTable")
		return GetDeviceTableFragment(c)
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

// encodeHxVals encodes device data as JSON and escapes it for HTML attribute use
func encodeHxVals(vendorID, productID string) string {
	data := map[string]string{
		"vendorId":  vendorID,
		"productId": productID,
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		// Fallback to manual encoding if JSON marshal fails
		return fmt.Sprintf(`{"vendorId":"%s","productId":"%s"}`, vendorID, productID)
	}
	// Escape for HTML attribute (single quotes in attribute, so only need to escape single quotes and backslashes)
	jsonStr := string(jsonBytes)
	jsonStr = strings.ReplaceAll(jsonStr, `\`, `\\`)
	jsonStr = strings.ReplaceAll(jsonStr, `'`, `\'`)
	return jsonStr
}

// DeviceTableData holds data needed to render device table rows
type DeviceTableData struct {
	USBDevices      []USBDeviceResponse
	AttachedDevices []AttachedDeviceResponse
	Favorites       []db.FavoriteDevice
	VMName          string
}

// GetDeviceTableFragment returns HTML fragment for device table rows
func GetDeviceTableFragment(c *fiber.Ctx) error {
	vmName := c.Query("vmName", "")
	if vmName == "" {
		vmName = c.FormValue("vmName", "")
	}
	// Also check URL params (for when called from attach/detach handlers)
	if vmName == "" {
		vmName = c.Params("vmName", "")
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
		return c.Status(500).SendString(fmt.Sprintf("<tr><td colspan='4' class='text-center text-error'>Failed to load USB devices: %v</td></tr>", usbErr))
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

	// Create a map for quick lookup of attached devices
	attachedMap := make(map[string]bool)
	for _, device := range attachedDevices {
		key := strings.ToLower(device.VendorID) + ":" + strings.ToLower(device.ProductID)
		attachedMap[key] = true
	}

	// Create a map for quick lookup of favorites
	favoriteMap := make(map[string]bool)
	for _, fav := range favorites {
		key := strings.ToLower(fav.VendorID) + ":" + strings.ToLower(fav.ProductID)
		favoriteMap[key] = true
	}

	// Build HTML for table rows
	var html strings.Builder
	for _, device := range usbDevices {
		deviceKey := strings.ToLower(device.VendorID) + ":" + strings.ToLower(device.ProductID)
		isAttached := attachedMap[deviceKey]
		isFavorite := favoriteMap[deviceKey]

		// Determine button class and text
		btnClass := "btn-primary"
		btnText := "Attach"
		if isAttached {
			btnClass = "btn-error"
			btnText = "Detach"
		}

		// Status badge
		statusClass := "badge-ghost"
		statusText := "Not Attached"
		if isAttached {
			statusClass = "badge-success"
			statusText = "Attached"
		}

		// Favorite button
		favClass := "btn-ghost"
		favIcon := "☆"
		if isFavorite {
			favClass = "btn-warning"
			favIcon = "⭐"
		}

		// Build the row HTML
		html.WriteString("<tr>")
		html.WriteString(fmt.Sprintf("<td class='font-mono text-sm'>%s:%s</td>", device.VendorID, device.ProductID))
		html.WriteString(fmt.Sprintf("<td>%s</td>", device.Description))
		html.WriteString(fmt.Sprintf("<td><span class='badge %s'>%s</span></td>", statusClass, statusText))

		// Actions column
		html.WriteString("<td><div class='flex gap-2'>")

		// Attach/Detach button
		if vmName != "" {
			// Use hx-vals to pass device data directly, properly encoded for JSON and HTML
			hxVals := encodeHxVals(device.VendorID, device.ProductID)

			if isAttached {
				html.WriteString(fmt.Sprintf(
					"<button class='btn btn-sm %s' hx-ext='json-enc' hx-post='/api/vms/%s/detach' hx-target='#device-table-body' hx-swap='innerHTML' hx-vals='%s' hx-disabled-elt='this'><span class='htmx-indicator'>⏳</span><span class='htmx-request-hide'>%s</span></button>",
					btnClass, vmName, hxVals, btnText,
				))
			} else {
				html.WriteString(fmt.Sprintf(
					"<button class='btn btn-sm %s' hx-ext='json-enc' hx-post='/api/vms/%s/attach' hx-target='#device-table-body' hx-swap='innerHTML' hx-vals='%s' hx-disabled-elt='this'><span class='htmx-indicator'>⏳</span><span class='htmx-request-hide'>%s</span></button>",
					btnClass, vmName, hxVals, btnText,
				))
			}
		} else {
			html.WriteString(fmt.Sprintf("<button class='btn btn-sm %s' disabled>%s</button>", btnClass, btnText))
		}

		// Favorite button - use Alpine.js @click
		// Note: Alpine.js will process this when content is swapped in
		favAction := "addFavorite"
		if isFavorite {
			favAction = "removeFavorite"
		}
		deviceObj := fmt.Sprintf("{vendorId: '%s', productId: '%s', description: '%s'}",
			strings.ReplaceAll(device.VendorID, "'", "\\'"),
			strings.ReplaceAll(device.ProductID, "'", "\\'"),
			strings.ReplaceAll(device.Description, "'", "\\'"))
		html.WriteString(fmt.Sprintf(
			"<button class='btn btn-sm %s' @click=\"%s(%s)\" title='%s'><span>%s</span></button>",
			favClass, favAction, deviceObj,
			map[bool]string{true: "Remove from favorites", false: "Add to favorites"}[isFavorite],
			favIcon,
		))

		html.WriteString("</div></td>")
		html.WriteString("</tr>")
	}

	if len(usbDevices) == 0 {
		html.WriteString("<tr><td colspan='4' class='text-center py-8 text-gray-500'>No USB devices found.</td></tr>")
	}

	return c.SendString(html.String())
}

// GetFavoritesTableFragment returns HTML fragment for favorites table rows
func GetFavoritesTableFragment(c *fiber.Ctx) error {
	vmName := c.Query("vmName", "")
	if vmName == "" {
		vmName = c.FormValue("vmName", "")
	}
	// Also check URL params (for when called from attach/detach handlers)
	if vmName == "" {
		vmName = c.Params("vmName", "")
	}

	// Run independent operations in parallel using goroutines
	var favorites []db.FavoriteDevice
	var attachedDevices []AttachedDeviceResponse

	var wg sync.WaitGroup
	var favoritesErr, attachedErr error

	// Get favorites
	wg.Add(1)
	go func() {
		defer wg.Done()
		favorites, favoritesErr = db.GetAllFavorites()
	}()

	// Get attached devices if VM is selected (can run in parallel with favorites)
	if vmName != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attachedDevices, attachedErr = getAttachedDevicesList(vmName)
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Handle errors
	if favoritesErr != nil {
		log.Printf("Warning: Failed to get favorites: %v", favoritesErr)
		return c.SendString("<tr><td colspan='3' class='text-center text-error'>Failed to load favorites</td></tr>")
	}

	if len(favorites) == 0 {
		return c.SendString("")
	}

	if attachedErr != nil {
		// Log error but continue - attached devices might not be available
		log.Printf("Warning: Failed to get attached devices for %s: %v", vmName, attachedErr)
		attachedDevices = []AttachedDeviceResponse{}
	}

	// Create a map for quick lookup of attached devices
	attachedMap := make(map[string]bool)
	for _, device := range attachedDevices {
		key := strings.ToLower(device.VendorID) + ":" + strings.ToLower(device.ProductID)
		attachedMap[key] = true
	}

	// Build HTML for table rows
	var html strings.Builder
	for _, fav := range favorites {
		deviceKey := strings.ToLower(fav.VendorID) + ":" + strings.ToLower(fav.ProductID)
		isAttached := attachedMap[deviceKey]

		// Determine button class and text
		btnClass := "btn-primary"
		btnText := "Attach"
		if isAttached {
			btnClass = "btn-error"
			btnText = "Detach"
		}

		// Build the row HTML
		html.WriteString("<tr>")
		html.WriteString(fmt.Sprintf("<td class='font-mono text-sm'>%s:%s</td>", fav.VendorID, fav.ProductID))
		html.WriteString(fmt.Sprintf("<td>%s</td>", fav.Description))

		// Actions column
		html.WriteString("<td><div class='flex gap-2'>")

		// Attach/Detach button
		if vmName != "" {
			// Use hx-vals to pass device data directly, properly encoded for JSON and HTML
			hxVals := encodeHxVals(fav.VendorID, fav.ProductID)

			if isAttached {
				html.WriteString(fmt.Sprintf(
					"<button class='btn btn-sm %s' hx-ext='json-enc' hx-post='/api/vms/%s/detach' hx-target='#device-table-body' hx-swap='innerHTML' hx-vals='%s' hx-disabled-elt='this' hx-trigger='refreshFavorites from:body'><span class='htmx-indicator'>⏳</span><span class='htmx-request-hide'>%s</span></button>",
					btnClass, vmName, hxVals, btnText,
				))
			} else {
				html.WriteString(fmt.Sprintf(
					"<button class='btn btn-sm %s' hx-ext='json-enc' hx-post='/api/vms/%s/attach' hx-target='#device-table-body' hx-swap='innerHTML' hx-vals='%s' hx-disabled-elt='this' hx-trigger='refreshFavorites from:body'><span class='htmx-indicator'>⏳</span><span class='htmx-request-hide'>%s</span></button>",
					btnClass, vmName, hxVals, btnText,
				))
			}
		} else {
			html.WriteString(fmt.Sprintf("<button class='btn btn-sm %s' disabled>%s</button>", btnClass, btnText))
		}

		// Remove favorite button - use Alpine.js @click
		favObj := fmt.Sprintf("{vendorId: '%s', productId: '%s'}",
			strings.ReplaceAll(fav.VendorID, "'", "\\'"),
			strings.ReplaceAll(fav.ProductID, "'", "\\'"))
		html.WriteString(fmt.Sprintf(
			"<button class='btn btn-sm btn-error' @click=\"removeFavorite(%s)\" title='Remove from favorites'>Remove</button>",
			favObj,
		))

		html.WriteString("</div></td>")
		html.WriteString("</tr>")
	}

	return c.SendString(html.String())
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
