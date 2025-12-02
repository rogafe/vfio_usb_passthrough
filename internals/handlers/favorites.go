package handlers

import (
	"vfio_usb_passthrough/internals/db"

	"github.com/gofiber/fiber/v2"
)

// GetFavorites returns all favorite devices
func GetFavorites(c *fiber.Ctx) error {
	favorites, err := db.GetAllFavorites()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to get favorites",
			"details": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"favorites": favorites,
	})
}

// AddFavoriteRequest represents a request to add a favorite
type AddFavoriteRequest struct {
	VendorID    string `json:"vendorId"`
	ProductID   string `json:"productId"`
	Description string `json:"description"`
}

// AddFavorite adds a device to favorites
func AddFavorite(c *fiber.Ctx) error {
	var req AddFavoriteRequest
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

	err := db.AddFavorite(req.VendorID, req.ProductID, req.Description)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to add favorite",
			"details": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Device added to favorites",
	})
}

// RemoveFavoriteRequest represents a request to remove a favorite
type RemoveFavoriteRequest struct {
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
}

// RemoveFavorite removes a device from favorites
func RemoveFavorite(c *fiber.Ctx) error {
	var req RemoveFavoriteRequest
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

	err := db.RemoveFavorite(req.VendorID, req.ProductID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error":   "Failed to remove favorite",
			"details": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Device removed from favorites",
	})
}
