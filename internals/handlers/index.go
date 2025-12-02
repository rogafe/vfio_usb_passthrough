package handlers

import (
	"github.com/gofiber/fiber/v2"
)

// GetIndex handles the main page request
func GetIndex(c *fiber.Ctx) error {

	return c.Render("index", fiber.Map{})
}
