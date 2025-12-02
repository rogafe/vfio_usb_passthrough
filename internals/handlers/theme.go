package handlers

import (
	"github.com/gofiber/fiber/v2"
)

func ToggleTheme(c *fiber.Ctx) error {
	cookie := c.Cookies("theme")
	if cookie == "light" || cookie == "" {
		c.Cookie(&fiber.Cookie{
			Name:  "theme",
			Value: "dark",
			Path:  "/",
		})
	} else {
		c.Cookie(&fiber.Cookie{
			Name:  "theme",
			Value: "light",
			Path:  "/",
		})
	}
	return c.SendStatus(fiber.StatusOK)
}
