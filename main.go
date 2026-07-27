package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/basicauth"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"

	"amneziawg-web-ui/internal"
)

//go:embed web-ui/static
var staticFiles embed.FS

//go:embed web-ui/templates/index.html
var indexHTML []byte

func main() {
	fmt.Println("AmneziaWG Web UI (Go/Fiber) starting...")

	// Initialise business logic
	mgr := internal.NewManager()

	// Initialise Socket.IO hub (registers connection handlers immediately)
	hub := internal.NewHub(mgr)
	mgr.SetHub(hub)

	// Start background goroutine for periodic traffic broadcasts
	go hub.StartTrafficUpdates()

	// Build Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if fe, ok := err.(*fiber.Error); ok {
				code = fe.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	app.Use(recover.New())
	app.Use(logger.New())

	// Basic auth protects everything except static assets and the health-check
	app.Use(basicauth.New(basicauth.Config{
		Next: func(c fiber.Ctx) bool {
			path := c.Path()
			return path == "/status" || strings.HasPrefix(path, "/static")
		},
		Users: map[string]string{
			webUIUser(): webUIPassword(),
		},
		Realm: "Restricted Content",
	}))

	// Socket.IO — must be registered before other routes.
	// adaptor.HTTPHandler wraps net/http.Handler; fasthttpadaptor under the hood
	// supports http.Hijacker so gorilla/websocket upgrades work correctly.
	app.Use("/socket.io/", adaptor.HTTPHandler(hub.Server().ServeHandler(nil)))

	// Static files served from embedded FS.
	// Route /static* → strip prefix → prepend root "web-ui/static" in the FS.
	app.Get("/static*", static.New("web-ui/static", static.Config{
		FS:     staticFiles,
		Browse: false,
	}))

	// Register REST routes (index.html served from embedded bytes)
	h := internal.NewHandlers(mgr, hub, indexHTML)
	h.RegisterRoutes(app)

	port := webUIPort()
	fmt.Printf("Listening on :%d\n", port)
	log.Fatal(app.Listen(":" + strconv.Itoa(port)))
}

func webUIPort() int {
	if s := os.Getenv("WEB_UI_PORT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 5000
}

func webUIUser() string {
	if s := os.Getenv("WEB_UI_USER"); s != "" {
		return s
	}
	return "admin"
}

func webUIPassword() string {
	if s := os.Getenv("WEB_UI_PASSWORD"); s != "" {
		return s
	}
	// SHA-256 hash of "changeme" in base64
	return "BXugPWxEEEhj3HNh/kV4ll0YhzYPkKCJWILlimJI/IY="
}
