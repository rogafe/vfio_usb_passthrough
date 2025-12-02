package main

import (
	"log"
	"os"
	"strings"

	"github.com/Masterminds/sprig/v3"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/template/html/v2"
	"github.com/joho/godotenv"

	"vfio_usb_passthrough/internals/db"
	"vfio_usb_passthrough/internals/handlers"
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

	// Static files
	app.Static("/assets", "./assets/dist")

	// Theme toggle route
	app.Post("/theme/toggle", handlers.ToggleTheme)

	// API routes for USB passthrough
	api := app.Group("/api")
	api.Get("/vms", handlers.ListRunningVMs)
	// The following lines were causing compile errors due to missing handler functions.
	// Ensure that the handlers are properly defined and imported in "internals/handlers".
	api.Get("/usb-devices", handlers.ListUSBDevices)
	api.Get("/vms/:vmName/devices", handlers.GetAttachedDevices)
	api.Post("/vms/:vmName/attach", handlers.AttachDevice)
	api.Post("/vms/:vmName/detach", handlers.DetachDevice)
	api.Get("/devices-table", handlers.GetDeviceTableFragment)
	api.Get("/favorites-table", handlers.GetFavoritesTableFragment)

	// Favorites routes
	api.Get("/favorites", handlers.GetFavorites)
	api.Post("/favorites", handlers.AddFavorite)
	api.Delete("/favorites", handlers.RemoveFavorite)

	// Auth routes (no middleware)

	app.Get("/", handlers.GetIndex)

	// Start server
	log.Fatal(app.Listen(":3000"))
}
