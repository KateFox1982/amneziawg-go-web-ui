package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/basicauth"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"

	"amneziawg-web-ui/internal"
	"amneziawg-web-ui/web-ui/wasm"
)

const socketIOPath = "/socket.io"

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

	// The wasm bundle is tens of megabytes uncompressed, so compression is
	// not optional here. Socket.IO is excluded: its long-polling responses
	// are tiny, and compressing them only gets in the way of the framing.
	app.Use(compress.New(compress.Config{
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), socketIOPath)
		},
	}))

	// Basic auth protects everything except static assets and the health-check
	app.Use(basicauth.New(basicauth.Config{
		Next: func(c fiber.Ctx) bool {
			// Only the container health check stays open; every asset of
			// the UI is behind the same credentials as the API.
			return c.Path() == "/status"
		},
		Users: map[string]string{
			webUIUser(): webUIPassword(),
		},
		Realm: "Restricted Content",
	}))

	// Socket.IO — must be registered before other routes.
	// adaptor.HTTPHandler wraps net/http.Handler; fasthttpadaptor under the hood
	// supports http.Hijacker so gorilla/websocket upgrades work correctly.
	app.Use(socketIOPath+"/", adaptor.HTTPHandler(hub.Server().ServeHandler(nil)))

	// REST routes first, so the catch-all below cannot shadow them.
	h := internal.NewHandlers(mgr, hub)
	h.RegisterRoutes(app)

	// Everything else is the embedded frontend: the loader page, the wasm
	// bundle and its assets. The files are embedded by the web-ui module
	// itself - go:embed cannot reach across a module boundary - and reach us
	// through the replace directive in go.mod.
	//
	// That embed covers the package directory, so the wrapper's own source
	// is in there too; nobody has any business fetching it.
	app.Get("/dist.go", func(fiber.Ctx) error { return fiber.ErrNotFound })

	app.Use(frontendCache(wasm.FS(), "."))
	app.Get("/*", static.New("", static.Config{
		FS:         wasm.FS(),
		IndexNames: []string{"index.html"},
		Browse:     false,
	}))

	port := webUIPort()
	fmt.Printf("Listening on :%d\n", port)
	log.Fatal(app.Listen(":" + strconv.Itoa(port)))
}

// frontendCache makes the browser revalidate the embedded frontend instead of
// caching it blindly. The file names are the same in every release, so
// without a content-derived validator a browser would happily keep running
// the previous bundle after an upgrade - and re-downloading ~50 MB of wasm on
// every page load is not an acceptable alternative.
func frontendCache(files fs.FS, root string) fiber.Handler {
	tags := map[string]string{}

	entries, err := fs.ReadDir(files, root)
	if err != nil {
		log.Printf("frontend cache: %v", err)
		return func(c fiber.Ctx) error { return c.Next() }
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		tag, err := fileETag(files, path.Join(root, entry.Name()))
		if err != nil {
			log.Printf("frontend cache: %v", err)
			continue
		}
		tags["/"+entry.Name()] = tag
	}
	tags["/"] = tags["/index.html"]

	return func(c fiber.Ctx) error {
		tag, ok := tags[c.Path()]
		if !ok || tag == "" || c.Method() != fiber.MethodGet {
			return c.Next()
		}

		c.Set(fiber.HeaderCacheControl, "no-cache")
		c.Set(fiber.HeaderETag, tag)
		if etagMatches(c.Get(fiber.HeaderIfNoneMatch), tag) {
			return c.SendStatus(fiber.StatusNotModified)
		}

		// go:embed stamps every file with the zero time, and the static
		// handler below answers any If-Modified-Since at or after it with a
		// 304 - which is every conditional request a browser will ever make,
		// upgraded bundle or not. The content hash is the only validator worth
		// anything here, so the modification time is dropped from both ends of
		// the exchange rather than left to contradict it.
		c.Request().Header.Del(fiber.HeaderIfModifiedSince)
		err := c.Next()
		c.Response().Header.Del(fiber.HeaderLastModified)
		return err
	}
}

// etagMatches reports whether an If-None-Match header covers tag. Browsers
// echo back what they were sent, but the header is a list and the weak marker
// is not part of the comparison, so both are handled.
func etagMatches(header, tag string) bool {
	tag = strings.TrimPrefix(tag, "W/")
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == tag {
			return true
		}
	}
	return false
}

func fileETag(files fs.FS, name string) (string, error) {
	file, err := files.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}

	// Weak on purpose. The tag hashes the file as embedded, while what goes
	// on the wire is usually gzipped - a difference a strong tag is not
	// allowed to gloss over. It also keeps the compression middleware from
	// replacing the tag with a hash of the compressed body: that one changes
	// with the compression settings, and it is the wrong thing to compare a
	// cached copy against.
	return `W/"` + hex.EncodeToString(sum.Sum(nil)[:8]) + `"`, nil
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
