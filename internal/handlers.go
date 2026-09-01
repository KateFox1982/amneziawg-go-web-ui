package internal

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"encoding/json"

	"github.com/gofiber/fiber/v3"
)

// Handlers holds dependencies for HTTP request handlers.
type Handlers struct {
	mgr *Manager
	hub Hub
}

// Hub interface for handler usage (broader than HubBroadcaster).
type Hub interface {
	HubBroadcaster
}

// NewHandlers creates a Handlers instance.
func NewHandlers(mgr *Manager, hub Hub) *Handlers {
	return &Handlers{mgr: mgr, hub: hub}
}

// RegisterRoutes attaches all API routes to the Fiber app.
func (h *Handlers) RegisterRoutes(app *fiber.App) {
	app.Get("/status", h.containerUptime)

	api := app.Group("/api")

	// Servers – static routes first
	api.Get("/servers/traffic", h.getAllServersTraffic)
	api.Get("/servers", h.getServers)
	api.Post("/servers", h.createServer)
	api.Delete("/servers/:id", h.deleteServer)
	api.Post("/servers/:id/start", h.startServer)
	api.Post("/servers/:id/stop", h.stopServer)
	api.Get("/servers/:id/config", h.getServerConfig)
	api.Get("/servers/:id/config/download", h.downloadServerConfig)
	api.Get("/servers/:id/info", h.getServerInfo)
	api.Get("/servers/:id/traffic", h.getServerTraffic)

	// Clients
	api.Get("/servers/:id/clients", h.getServerClients)
	api.Post("/servers/:id/clients", h.addClient)
	api.Delete("/servers/:id/clients/:clientId", h.deleteClient)
	api.Put("/servers/:id/clients/:clientId/allowed-ips", h.updateClientAllowedIPs)
	api.Put("/servers/:id/clients/:clientId/i-settings", h.updateClientISettings)
	api.Get("/servers/:id/clients/:clientId/config", h.downloadClientConfig)
	api.Get("/servers/:id/clients/:clientId/config-both", h.getClientConfigBoth)
	api.Get("/servers/:id/clients/:clientId/link", h.getClientAmneziaLink)
	api.Post("/servers/:id/clients/:clientId/suspend", h.suspendClient)
	api.Post("/servers/:id/clients/:clientId/activate", h.activateClient)
	api.Put("/servers/:id/clients/:clientId/suspend-time", h.updateClientSuspendTime)

	// Misc
	api.Get("/clients", h.getAllClients)
	api.Get("/default-i-settings", h.getDefaultISettings)
	api.Get("/system/status", h.systemStatus)
	api.Get("/system/refresh-ip", h.refreshIP)
	api.Get("/system/iptables-test", h.iptablesTest)
}

func (h *Handlers) containerUptime(c fiber.Ctx) error {
	out, err := exec.Command("stat", "-c", "%Y", "/proc/1/cmdline").Output()
	if err != nil {
		return c.SendString("Container Uptime: unknown")
	}
	epoch, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	uptime := time.Now().Unix() - epoch
	d := uptime / 86400
	h2 := (uptime % 86400) / 3600
	m2 := (uptime % 3600) / 60
	s2 := uptime % 60
	return c.SendString(fmt.Sprintf("Container Uptime: %dd %dh %dm %ds", d, h2, m2, s2))
}

// ── Servers ──────────────────────────────────────────────────────────────────

func (h *Handlers) getServers(c fiber.Ctx) error {
	return c.JSON(h.mgr.GetServersWithStatus())
}

func (h *Handlers) createServer(c fiber.Ctx) error {
	var req CreateServerRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON: " + err.Error()})
	}
	srv, err := h.mgr.CreateServer(req)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(srv)
}

func (h *Handlers) deleteServer(c fiber.Ctx) error {
	id := c.Params("id")
	if h.mgr.DeleteServer(id) {
		return c.JSON(fiber.Map{"status": "deleted", "server_id": id})
	}
	return c.Status(404).JSON(fiber.Map{"error": "server not found"})
}

func (h *Handlers) startServer(c fiber.Ctx) error {
	id := c.Params("id")
	if h.mgr.StartServer(id) {
		return c.JSON(fiber.Map{"status": "started"})
	}
	return c.Status(404).JSON(fiber.Map{"error": "server not found or failed to start"})
}

func (h *Handlers) stopServer(c fiber.Ctx) error {
	id := c.Params("id")
	if h.mgr.StopServer(id) {
		return c.JSON(fiber.Map{"status": "stopped"})
	}
	return c.Status(404).JSON(fiber.Map{"error": "server not found or failed to stop"})
}

func (h *Handlers) getServerConfig(c fiber.Ctx) error {
	id := c.Params("id")
	srv := h.mgr.getServer(id)
	if srv == nil {
		return c.Status(404).JSON(fiber.Map{"error": "server not found"})
	}
	data, err := os.ReadFile(srv.ConfigPath)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "config file not found"})
	}
	return c.JSON(fiber.Map{
		"server_id":      id,
		"server_name":    srv.Name,
		"config_path":    srv.ConfigPath,
		"config_content": string(data),
		"interface":      srv.Interface,
		"public_key":     srv.ServerPublicKey,
	})
}

func (h *Handlers) downloadServerConfig(c fiber.Ctx) error {
	id := c.Params("id")
	srv := h.mgr.getServer(id)
	if srv == nil {
		return c.Status(404).JSON(fiber.Map{"error": "server not found"})
	}
	if _, err := os.Stat(srv.ConfigPath); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "config file not found"})
	}
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"`, srv.Interface))
	return c.SendFile(srv.ConfigPath)
}

func (h *Handlers) getServerInfo(c fiber.Ctx) error {
	id := c.Params("id")
	srv := h.mgr.getServer(id)
	if srv == nil {
		return c.Status(404).JSON(fiber.Map{"error": "server not found"})
	}

	status := h.mgr.GetServerStatus(id)

	preview := ""
	if data, err := os.ReadFile(srv.ConfigPath); err == nil {
		lines := strings.Split(string(data), "\n")
		if len(lines) > 10 {
			lines = lines[:10]
		}
		preview = strings.Join(lines, "\n")
	}

	return c.JSON(fiber.Map{
		"id":                  srv.ID,
		"name":                srv.Name,
		"protocol":            srv.Protocol,
		"port":                srv.Port,
		"status":              status,
		"interface":           srv.Interface,
		"config_path":         srv.ConfigPath,
		"public_ip":           srv.PublicIP,
		"server_ip":           srv.ServerIP,
		"subnet":              srv.Subnet,
		"mtu":                 srv.MTU,
		"obfuscation_enabled": srv.ObfuscationEnabled,
		"obfuscation_params":  srv.ObfuscationParams,
		"clients_count":       len(srv.Clients),
		"clients":             srv.Clients,
		"created_at":          srv.CreatedAt,
		"config_preview":      preview,
		"public_key":          srv.ServerPublicKey,
		"dns":                 srv.DNS,
		"default_i_settings": fiber.Map{
			"i1": DefaultI1, "i2": DefaultI2,
			"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
		},
	})
}

func (h *Handlers) getServerTraffic(c fiber.Ctx) error {
	id := c.Params("id")
	traffic := h.mgr.GetPeerTrafficForServer(id)
	if traffic == nil {
		return c.Status(404).JSON(fiber.Map{"error": "server not found or no traffic data"})
	}
	return c.JSON(traffic)
}

func (h *Handlers) getAllServersTraffic(c fiber.Ctx) error {
	return c.JSON(h.mgr.GetAllServersTraffic())
}

// ── Clients ──────────────────────────────────────────────────────────────────

func (h *Handlers) getServerClients(c fiber.Ctx) error {
	id := c.Params("id")
	return c.JSON(h.mgr.GetClientConfigs(id))
}

func (h *Handlers) getAllClients(c fiber.Ctx) error {
	return c.JSON(h.mgr.GetClientConfigs(""))
}

func (h *Handlers) addClient(c fiber.Ctx) error {
	id := c.Params("id")
	var req AddClientRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
	}
	if req.Name == "" {
		req.Name = "New Client"
	}
	if req.AllowedIPs == "" {
		req.AllowedIPs = "0.0.0.0/0, ::/0"
	}

	client, configContent, err := h.mgr.AddClient(id, req.Name, req.ApplyISettings, req.ISettings, req.AllowedIPs)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"client": client, "config": configContent})
}

func (h *Handlers) deleteClient(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	if h.mgr.DeleteClient(id, clientID) {
		return c.JSON(fiber.Map{"status": "deleted", "client_id": clientID})
	}
	return c.Status(404).JSON(fiber.Map{"error": "client not found"})
}

func (h *Handlers) updateClientAllowedIPs(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	var req UpdateAllowedIPsRequest
	json.Unmarshal(c.Body(), &req)
	if req.AllowedIPs == "" {
		req.AllowedIPs = "0.0.0.0/0, ::/0"
	}
	client, cfg, err := h.mgr.UpdateClientAllowedIPs(id, clientID, req.AllowedIPs)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"client": client, "config": cfg})
}

func (h *Handlers) updateClientISettings(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	var req UpdateISettingsRequest
	json.Unmarshal(c.Body(), &req)
	client, cfg, err := h.mgr.UpdateClientISettings(id, clientID, req.ApplyISettings, req.ISettings)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"client": client, "config": cfg})
}

func (h *Handlers) downloadClientConfig(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	gc, ok := h.mgr.getClientInServer(id, clientID)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "client not found"})
	}

	configContent := h.mgr.GenerateClientConfig(id, &gc, true)
	filename := fmt.Sprintf("%s_%s.conf", gc.Name, gc.ServerName)
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.SendString(configContent)
}

func (h *Handlers) getClientAmneziaLink(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	gc, ok := h.mgr.getClientInServer(id, clientID)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "client not found"})
	}

	vpnURL, err := h.mgr.GenerateAmneziaVpnURL(id, &gc)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"vpn_url": vpnURL})
}

func (h *Handlers) getClientConfigBoth(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	gc, ok := h.mgr.getClientInServer(id, clientID)
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "client not found"})
	}

	clean := h.mgr.GenerateClientConfig(id, &gc, false)
	full := h.mgr.GenerateClientConfig(id, &gc, true)

	var suspendAtReadable interface{}
	if gc.SuspendAt != nil {
		suspendAtReadable = time.Unix(int64(*gc.SuspendAt), 0).UTC().String()
	}

	return c.JSON(fiber.Map{
		"server_id":           id,
		"client_id":           clientID,
		"client_name":         gc.Name,
		"clean_config":        clean,
		"full_config":         full,
		"clean_length":        len(clean),
		"full_length":         len(full),
		"created_at":          gc.CreatedAt,
		"created_at_readable": time.Unix(int64(gc.CreatedAt), 0).UTC().String(),
		"suspend_at":          gc.SuspendAt,
		"suspend_at_readable": suspendAtReadable,
	})
}

func (h *Handlers) suspendClient(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	ok, msg := h.mgr.SuspendClient(id, clientID)
	if ok {
		return c.JSON(fiber.Map{"status": "suspended", "client_id": clientID, "message": msg})
	}
	return c.Status(404).JSON(fiber.Map{"error": msg})
}

func (h *Handlers) activateClient(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")
	ok, msg := h.mgr.ActivateClient(id, clientID)
	if ok {
		return c.JSON(fiber.Map{"status": "activated", "client_id": clientID, "message": msg})
	}
	return c.Status(404).JSON(fiber.Map{"error": msg})
}

func (h *Handlers) updateClientSuspendTime(c fiber.Ctx) error {
	id := c.Params("id")
	clientID := c.Params("clientId")

	var req UpdateSuspendTimeRequest
	json.Unmarshal(c.Body(), &req)

	var ts *float64
	if req.SuspendAt != nil && *req.SuspendAt != "" {
		t, err := time.Parse(time.RFC3339, *req.SuspendAt)
		if err != nil {
			// Try without timezone
			t, err = time.Parse("2006-01-02T15:04:05", *req.SuspendAt)
			if err != nil {
				return c.Status(400).JSON(fiber.Map{"error": "invalid datetime format"})
			}
		}
		v := float64(t.Unix())
		ts = &v
	}

	client, msg, err := h.mgr.UpdateClientSuspendTime(id, clientID, ts)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"client": client, "message": msg})
}

// ── Misc ─────────────────────────────────────────────────────────────────────

func (h *Handlers) getDefaultISettings(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"i1": DefaultI1, "i2": DefaultI2,
		"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
	})
}

func (h *Handlers) systemStatus(c fiber.Ctx) error {
	servers := h.mgr.copyServers()
	total := len(servers)
	totalClients := h.mgr.clientCount()

	active := 0
	for _, s := range servers {
		if h.mgr.GetServerStatus(s.ID) == "running" {
			active++
		}
	}

	_, awgErr := os.Stat("/usr/bin/awg")
	_, awgQuickErr := os.Stat("/usr/bin/awg-quick")

	return c.JSON(fiber.Map{
		"awg_available":  awgErr == nil && awgQuickErr == nil,
		"public_ip":      h.mgr.PublicIP,
		"total_servers":  total,
		"total_clients":  totalClients,
		"active_servers": active,
		"timestamp":      float64(time.Now().Unix()),
		"environment": fiber.Map{
			"web_ui_port":        h.mgr.WebUIPort,
			"auto_start_servers": h.mgr.AutoStart,
			"default_mtu":        h.mgr.DefaultMTU,
			"default_subnet":     h.mgr.DefaultSubnet,
			"default_port":       h.mgr.DefaultPort,
			"default_dns":        strings.Join(h.mgr.DNSServers, ","),
		},
	})
}

func (h *Handlers) refreshIP(c fiber.Ctx) error {
	ip := h.mgr.RefreshPublicIP()
	return c.JSON(fiber.Map{"public_ip": ip})
}

func (h *Handlers) iptablesTest(c fiber.Ctx) error {
	serverID := c.Query("server_id")
	if serverID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "server_id parameter required"})
	}
	srv := h.mgr.getServer(serverID)
	if srv == nil {
		return c.Status(404).JSON(fiber.Map{"error": "server not found"})
	}

	checks := []string{
		fmt.Sprintf("iptables -L INPUT -n | grep %s", srv.Interface),
		fmt.Sprintf("iptables -L FORWARD -n | grep %s", srv.Interface),
		fmt.Sprintf("iptables -t nat -L POSTROUTING -n | grep %s", srv.Subnet),
	}
	results := map[string]string{}
	for _, cmd := range checks {
		out, err := execCommand(cmd)
		if err == nil && out != "" {
			results[cmd] = "Found"
		} else {
			results[cmd] = "Not found"
		}
	}

	return c.JSON(fiber.Map{
		"server_id":      serverID,
		"server_name":    srv.Name,
		"interface":      srv.Interface,
		"subnet":         srv.Subnet,
		"iptables_check": results,
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────
