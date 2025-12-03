package main

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/Masterminds/sprig/v3"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/template/html/v2"
	"github.com/joho/godotenv"

	"vfio_usb_passthrough/internals/db"
	"vfio_usb_passthrough/internals/handlers"
	"vfio_usb_passthrough/internals/middleware"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("vfio_usb_passthrough: ")
	log.SetOutput(os.Stdout)

	// if ENV is set to dev use godotenv
	env := os.Getenv("ENV")
	env = strings.ToLower(env)
	log.Println("ENV: ", env)
	if strings.Contains(env, "dev") {
		log.Println("Loading .env file")
		err := godotenv.Load()
		if err != nil {
			log.Fatalf("Error loading .env file: %v", err)
		}
	}
}

func main() {
	// Initialize database
	if err := db.InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Initialize template engine
	engine := html.New("./views", ".html")

	engine.AddFuncMap(sprig.FuncMap())

	engine.Debug(true)
	// Create app
	app := fiber.New(fiber.Config{
		Views:       engine,
		ViewsLayout: "layouts/base",
	})

	// add a middleware to log the request
	app.Use(logger.New())

	// Initialize and apply IP filter middleware
	ipFilter, err := middleware.NewIPFilterMiddleware()
	if err != nil {
		log.Fatalf("Failed to initialize IP filter middleware: %v", err)
	}
	app.Use(ipFilter)

	// Static files
	app.Static("/assets", "./assets/dist")

	// Theme toggle route
	app.Post("/theme/toggle", handlers.ToggleTheme)

	// API routes for USB passthrough with rate limiting
	api := app.Group("/api")

	// Apply rate limiting: 20 requests per minute per IP
	api.Use(limiter.New(limiter.Config{
		Max:        20,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			log.Printf("Rate limit exceeded for IP: %s", c.IP())
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Rate limit exceeded. Please try again later.",
			})
		},
	}))

	api.Get("/vms", handlers.ListRunningVMs)
	// The following lines were causing compile errors due to missing handler functions.
	// Ensure that the handlers are properly defined and imported in "internals/handlers".
	api.Get("/usb-devices", handlers.ListUSBDevices)
	api.Get("/vms/:vmName/devices", handlers.GetAttachedDevices)
	api.Post("/vms/:vmName/attach", handlers.AttachDevice)
	api.Post("/vms/:vmName/detach", handlers.DetachDevice)
	api.Get("/devices-state", handlers.GetDevicesState)

	// Favorites routes
	api.Get("/favorites", handlers.GetFavorites)
	api.Post("/favorites", handlers.AddFavorite)
	api.Delete("/favorites", handlers.RemoveFavorite)

	// Auth routes (no middleware)

	app.Get("/", handlers.GetIndex)

	// Start server with configurable bind address
	bindAddr := middleware.GetBindAddr()
	log.Printf("Starting server on %s", bindAddr)
	log.Fatal(app.Listen(bindAddr))
}
