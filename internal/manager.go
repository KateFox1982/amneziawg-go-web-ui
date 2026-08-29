package internal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager orchestrates all AmneziaWG operations.
type Manager struct {
	Config   *AppConfig
	PublicIP string
	hub      HubBroadcaster

	// Traffic update interval in seconds
	TrafficUpdateInterval int
	SuspendUpdateInterval int

	// Environment-driven defaults
	WebUIPort         string
	AutoStart         bool
	DefaultMTU        int
	DefaultSubnet     string
	DefaultPort       int
	DNSServers        []string
	EnableObfuscation bool

	mu sync.RWMutex
}

// HubBroadcaster is a minimal interface the Manager uses to send events.
type HubBroadcaster interface {
	BroadcastServerStatus(serverID, status string)
}

func getenv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// NewManager creates and initialises a Manager from environment variables.
func NewManager() *Manager {
	m := &Manager{
		TrafficUpdateInterval: 5,
		SuspendUpdateInterval: 60,
		EnableObfuscation:     true,
	}

	m.WebUIPort = getenv("WEB_UI_PORT", "80")
	m.AutoStart = strings.EqualFold(getenv("AUTO_START_SERVERS", "true"), "true")
	m.DefaultMTU = atoiDefault(getenv("DEFAULT_MTU", "1280"), 1280)
	m.DefaultSubnet = getenv("DEFAULT_SUBNET", "10.0.0.0/24")
	m.DefaultPort = atoiDefault(getenv("DEFAULT_PORT", "51834"), 51834)

	defaultDNS := getenv("DEFAULT_DNS", "8.8.8.8,1.1.1.1")
	for _, dns := range strings.Split(defaultDNS, ",") {
		if s := strings.TrimSpace(dns); s != "" {
			m.DNSServers = append(m.DNSServers, s)
		}
	}

	m.Config = m.loadConfig()
	m.ensureDirectories()
	m.migrateExistingServersToAwg31()
	m.PublicIP = m.detectPublicIP()

	if m.AutoStart {
		m.autoStartServers()
	}

	go m.startSuspensionChecker()

	fmt.Printf("=== Environment Configuration ===\n")
	fmt.Printf("WEB_UI_PORT: %s\n", m.WebUIPort)
	fmt.Printf("AUTO_START: %v\n", m.AutoStart)
	fmt.Printf("DEFAULT_MTU: %d\n", m.DefaultMTU)
	fmt.Printf("DEFAULT_SUBNET: %s\n", m.DefaultSubnet)
	fmt.Printf("DEFAULT_PORT: %d\n", m.DefaultPort)
	fmt.Printf("DNS_SERVERS: %v\n", m.DNSServers)
	fmt.Printf("Detected public IP: %s\n", m.PublicIP)

	return m
}

// SetHub injects the WebSocket hub so the manager can broadcast events.
func (m *Manager) SetHub(h HubBroadcaster) {
	m.hub = h
}

func (m *Manager) ensureDirectories() {
	os.MkdirAll(ConfigDir, 0o755)
	os.MkdirAll(WireguardConfigDir, 0o755)
	os.MkdirAll("/var/log/amnezia", 0o755)
}

func (m *Manager) detectPublicIP() string {
	services := []string{
		"http://ifconfig.me",
		"https://api.ipify.org",
		"https://ident.me",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, svc := range services {
		resp, err := client.Get(svc)
		if err != nil {
			continue
		}
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		ip := strings.TrimSpace(string(buf[:n]))
		if isValidIPv4(ip) {
			return ip
		}
	}
	// Fallback: local routing table
	out, err := execCommand("ip route get 1 | awk '{print $7}' | head -1")
	if err == nil && isValidIPv4(out) {
		return out
	}
	return "YOUR_SERVER_IP"
}

func isValidIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

func (m *Manager) autoStartServers() {
	fmt.Println("Checking for existing servers to auto-start...")
	for i := range m.Config.Servers {
		srv := &m.Config.Servers[i]
		if _, err := os.Stat(srv.ConfigPath); err == nil {
			if m.GetServerStatus(srv.ID) == "stopped" && srv.AutoStart {
				fmt.Printf("Auto-starting server: %s\n", srv.Name)
				m.StartServer(srv.ID)
			}
		}
	}
}

func (m *Manager) loadConfig() *AppConfig {
	data, err := os.ReadFile(ConfigFile)
	if err == nil {
		var cfg AppConfig
		if err = json.Unmarshal(data, &cfg); err == nil {
			if cfg.Clients == nil {
				cfg.Clients = make(map[string]Client)
			}
			for i := range cfg.Servers {
				if cfg.Servers[i].Clients == nil {
					cfg.Servers[i].Clients = []Client{}
				}
				if cfg.Servers[i].UnboundNATIPs == nil {
					cfg.Servers[i].UnboundNATIPs = []string{}
				}
			}
			migrateLegacyClientMap(&cfg)
			return &cfg
		} else {
			fmt.Printf("Error unmarshaling config: %v\n", err)
		}
	}
	return &AppConfig{
		Servers: []Server{},
		Clients: make(map[string]Client),
	}
}

func (m *Manager) SaveConfig() {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := json.MarshalIndent(m.Config, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling config: %v\n", err)
		return
	}
	if err := os.WriteFile(ConfigFile, data, 0o600); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
	}
}

func execCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) generateWireguardKeys() (privKey, pubKey string, err error) {
	priv, err := execCommand("awg genkey")
	if err != nil {
		return "", "", err
	}
	pub, err := execCommand(fmt.Sprintf("echo '%s' | awg pubkey", priv))
	if err != nil {
		return "", "", err
	}
	return priv, pub, nil
}

func (m *Manager) generatePresharedKey() string {
	key, err := execCommand("awg genpsk")
	if err != nil {
		b := make([]byte, 32)
		rand.Read(b) //nolint:gosec
		return base64.StdEncoding.EncodeToString(b)
	}
	return key
}

func randomBase64Key() string {
	b := make([]byte, 32)
	rand.Read(b) //nolint:gosec
	return base64.StdEncoding.EncodeToString(b)
}

// awg3MinPadding is the minimum value AmneziaWG 3.0 requires for S1-S4 when
// header protection is enabled: the cipher's 12-byte nonce is taken from the
// start of the padding, so anything smaller can't fit it.
const awg3MinPadding = 12

// generateObfuscationParams generates a full set of AmneziaWG 3.1 obfuscation
// parameters, including a header protection key. Obfuscation in this app is
// always AmneziaWG 3.x - there's no more separate 1.0/1.5/2.0 mode.
func (m *Manager) generateObfuscationParams(mtu int) ObfuscationParams {
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

	s1Max := mtu - 148
	if s1Max > 150 {
		s1Max = 150
	}
	s1 := rng.Intn(s1Max-15+1) + 15

	s2Max := mtu - 92
	if s2Max > 150 {
		s2Max = 150
	}
	var s2Candidates []int
	for s := 15; s <= s2Max; s++ {
		if s != s1+56 {
			s2Candidates = append(s2Candidates, s)
		}
	}
	s2 := s2Candidates[rng.Intn(len(s2Candidates))]

	jmin := rng.Intn(mtu-2-4+1) + 4
	jmax := jmin + 1 + rng.Intn(mtu-jmin)

	return ObfuscationParams{
		Jc:                  rng.Intn(9) + 4,
		Jmin:                jmin,
		Jmax:                jmax,
		S1:                  s1,
		S2:                  s2,
		S3:                  rng.Intn(256-awg3MinPadding+1) + awg3MinPadding,
		S4:                  rng.Intn(32-awg3MinPadding+1) + awg3MinPadding,
		H1:                  rng.Intn(90001) + 10000,
		H2:                  rng.Intn(100001) + 100000,
		H3:                  rng.Intn(100001) + 200000,
		H4:                  rng.Intn(100001) + 300000,
		MTU:                 mtu,
		HeaderProtectionKey: randomBase64Key(),
		RandomTrailers:      true,
		DisableCookies:      true,
	}
}

// validateObfuscationParams checks the AmneziaWG 3.0 header protection
// requirement: S1-S4 must all be at least awg3MinPadding, since the cipher's
// nonce is taken from the start of that padding. It also validates the
// format of the optional client-side range tuning knobs, if any were
// provided.
func validateObfuscationParams(p *ObfuscationParams) error {
	if p == nil {
		return fmt.Errorf("obfuscation parameters are required")
	}
	for name, v := range map[string]int{"S1": p.S1, "S2": p.S2, "S3": p.S3, "S4": p.S4} {
		if v < awg3MinPadding {
			return fmt.Errorf("%s must be at least %d for AmneziaWG 3.0 header protection, got %d", name, awg3MinPadding, v)
		}
	}
	for name, v := range map[string]string{
		"ContentPaddingAddition": p.ContentPaddingAddition,
		"RekeyAfterTime":         p.RekeyAfterTime,
		"RekeyTimeout":           p.RekeyTimeout,
		"RejectAfterTime":        p.RejectAfterTime,
		"KeepaliveTimeout":       p.KeepaliveTimeout,
		"MaxHandshakeAttempts":   p.MaxHandshakeAttempts,
		"PersistentKeepalive":    p.PersistentKeepalive,
	} {
		if v != "" && !isValidUintRange(v) {
			return fmt.Errorf("%s must be an integer or an \"a-b\" range, got %q", name, v)
		}
	}
	return nil
}

// isValidUintRange reports whether s is a plain non-negative integer or an
// "a-b" range of them, the format amneziawg-tools expects for its AWG 3.0
// range-typed config keys.
func isValidUintRange(s string) bool {
	return uintRangeRe.MatchString(s)
}

var uintRangeRe = regexp.MustCompile(`^\d+(-\d+)?$`)

// writeIfSet writes "key = value\n" to sb only if value is non-empty.
func writeIfSet(sb *strings.Builder, key, value string) {
	if value != "" {
		fmt.Fprintf(sb, "%s = %s\n", key, value)
	}
}

// writeAwgBoolIfOn writes an AmneziaWG 3.1 "key = on" line, and writes
// nothing when the flag is off. amneziawg-tools accepts on/off and 0/1, but
// an omitted key is the only form every pre-3.1 parser tolerates - clients
// still on AmneziaWG 3.0 reject an unknown key outright and would fail to
// import the config, so "off" is expressed by silence.
func writeAwgBoolIfOn(sb *strings.Builder, key string, enabled bool) {
	if enabled {
		fmt.Fprintf(sb, "%s = on\n", key)
	}
}

// awgProtocolVersion is the "protocol_version" value the AmneziaVPN app uses
// for the AmneziaWG 3 generation (protocols::awg::awgV3 in amnezia-client).
const awgProtocolVersion = "3.1"

// awgBoolOrEmpty renders an AmneziaWG 3.1 toggle the way the AmneziaVPN app's
// JSON expects it, or "" for off so the key can be dropped entirely.
func awgBoolOrEmpty(enabled bool) string {
	if enabled {
		return "on"
	}
	return ""
}

func getServerIP(network string) string {
	parts := strings.Split(network, ".")
	if len(parts) == 4 {
		return fmt.Sprintf("%s.%s.%s.1", parts[0], parts[1], parts[2])
	}
	return "10.0.0.1"
}

// CreateServer creates a new WireGuard server configuration.
func (m *Manager) CreateServer(req CreateServerRequest) (*Server, error) {
	name := req.Name
	if name == "" {
		name = "New Server"
	}
	port := req.Port
	if port == 0 {
		port = m.DefaultPort
	}
	subnet := req.Subnet
	if subnet == "" {
		subnet = m.DefaultSubnet
	}
	mtu := req.MTU
	if mtu == 0 {
		mtu = m.DefaultMTU
	}
	if mtu < 1280 || mtu > 1440 {
		return nil, fmt.Errorf("MTU must be between 1280 and 1440, got %d", mtu)
	}

	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		endpoint = m.PublicIP
	}

	// Parse DNS
	var dnsServers []string
	if req.DNS != nil {
		switch v := req.DNS.(type) {
		case string:
			for _, d := range strings.Split(v, ",") {
				if s := strings.TrimSpace(d); s != "" {
					dnsServers = append(dnsServers, s)
				}
			}
		case []interface{}:
			for _, d := range v {
				if s, ok := d.(string); ok {
					if t := strings.TrimSpace(s); t != "" {
						dnsServers = append(dnsServers, t)
					}
				}
			}
		}
	}
	if len(dnsServers) == 0 {
		dnsServers = m.DNSServers
	}
	for _, dns := range dnsServers {
		if !isValidIPv4(dns) {
			return nil, fmt.Errorf("invalid DNS server IP: %s", dns)
		}
	}

	enableObfuscation := m.EnableObfuscation
	if req.Obfuscation != nil {
		enableObfuscation = *req.Obfuscation
	}

	autoStart := m.AutoStart
	if req.AutoStart != nil {
		autoStart = *req.AutoStart
	}

	serverID := uuid.New().String()[:6]
	ifaceName := "wg-" + serverID
	configPath := filepath.Join(WireguardConfigDir, ifaceName+".conf")

	privKey, pubKey, err := m.generateWireguardKeys()
	if err != nil {
		privKey = randomBase64Key()
		pubKey = randomBase64Key()
	}

	// AmneziaWG 1.0/1.5/2.0-only modes are no longer supported: enabling
	// obfuscation always means the full AmneziaWG 3.x parameter set,
	// including mandatory header protection.
	var obfParams *ObfuscationParams
	if enableObfuscation {
		if req.ObfuscationParams != nil {
			obfParams = req.ObfuscationParams
		} else {
			p := m.generateObfuscationParams(mtu)
			obfParams = &p
		}
		if obfParams.HeaderProtectionKey == "" {
			obfParams.HeaderProtectionKey = randomBase64Key()
		}
		if err := validateObfuscationParams(obfParams); err != nil {
			return nil, err
		}
	}

	parts := strings.SplitN(subnet, "/", 2)
	network := parts[0]
	prefix := "24"
	if len(parts) > 1 {
		prefix = parts[1]
	}
	serverIP := getServerIP(network)

	// Build config file content
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Interface]\n")
	fmt.Fprintf(&sb, "PrivateKey = %s\n", privKey)
	fmt.Fprintf(&sb, "Address = %s/%s\n", serverIP, prefix)
	fmt.Fprintf(&sb, "ListenPort = %d\n", port)
	fmt.Fprintf(&sb, "SaveConfig = false\n")
	fmt.Fprintf(&sb, "MTU = %d\n", mtu)

	if enableObfuscation && obfParams != nil {
		fmt.Fprintf(&sb, "Jc = %d\n", obfParams.Jc)
		fmt.Fprintf(&sb, "Jmin = %d\n", obfParams.Jmin)
		fmt.Fprintf(&sb, "Jmax = %d\n", obfParams.Jmax)
		fmt.Fprintf(&sb, "S1 = %d\n", obfParams.S1)
		fmt.Fprintf(&sb, "S2 = %d\n", obfParams.S2)
		fmt.Fprintf(&sb, "S3 = %d\n", obfParams.S3)
		fmt.Fprintf(&sb, "S4 = %d\n", obfParams.S4)
		fmt.Fprintf(&sb, "H1 = %d\n", obfParams.H1)
		fmt.Fprintf(&sb, "H2 = %d\n", obfParams.H2)
		fmt.Fprintf(&sb, "H3 = %d\n", obfParams.H3)
		fmt.Fprintf(&sb, "H4 = %d\n", obfParams.H4)
		if obfParams.HeaderProtectionKey != "" {
			fmt.Fprintf(&sb, "HeaderProtectionKey = %s\n", obfParams.HeaderProtectionKey)
		}
		writeIfSet(&sb, "ContentPaddingAddition", obfParams.ContentPaddingAddition)
		writeIfSet(&sb, "RekeyAfterTime", obfParams.RekeyAfterTime)
		writeIfSet(&sb, "RekeyTimeout", obfParams.RekeyTimeout)
		writeIfSet(&sb, "RejectAfterTime", obfParams.RejectAfterTime)
		writeIfSet(&sb, "KeepaliveTimeout", obfParams.KeepaliveTimeout)
		writeIfSet(&sb, "MaxHandshakeAttempts", obfParams.MaxHandshakeAttempts)
		writeAwgBoolIfOn(&sb, "RandomTrailers", obfParams.RandomTrailers)
		writeAwgBoolIfOn(&sb, "DisableCookies", obfParams.DisableCookies)
	}

	if err := os.WriteFile(configPath, []byte(sb.String()), 0o600); err != nil {
		return nil, fmt.Errorf("failed to write config: %w", err)
	}

	srv := Server{
		ID:                 serverID,
		Name:               name,
		Protocol:           "wireguard",
		Port:               port,
		Status:             "stopped",
		Interface:          ifaceName,
		ConfigPath:         configPath,
		ServerPublicKey:    pubKey,
		ServerPrivateKey:   privKey,
		Subnet:             subnet,
		ServerIP:           serverIP,
		MTU:                mtu,
		PublicIP:           endpoint,
		Endpoint:           endpoint,
		ObfuscationEnabled: enableObfuscation,
		ObfuscationParams:  obfParams,
		AutoStart:          autoStart,
		DNS:                dnsServers,
		Clients:            []Client{},
		UnboundNATIPs:      []string{},
		CreatedAt:          float64(time.Now().Unix()),
	}

	m.addServer(srv)
	m.SaveConfig()

	if autoStart {
		fmt.Printf("Auto-starting new server: %s\n", name)
		m.StartServer(serverID)
	}

	return &srv, nil
}

// DeleteServer stops and removes a server and its clients.
func (m *Manager) DeleteServer(serverID string) bool {
	srv := m.getServer(serverID)
	if srv == nil {
		return false
	}

	if srv.Status == "running" {
		m.StopServer(serverID)
	}

	return m.removeServerLocked(serverID)
}

// removeServerLocked removes the server config file and all associated data.
func (m *Manager) removeServerLocked(serverID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, s := range m.Config.Servers {
		if s.ID == serverID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	srv := m.Config.Servers[idx]

	os.Remove(srv.ConfigPath)

	newClients := make(map[string]Client)
	for k, c := range m.Config.Clients {
		if c.ServerID != serverID {
			newClients[k] = c
		}
	}
	m.Config.Clients = newClients
	m.Config.Servers = append(m.Config.Servers[:idx], m.Config.Servers[idx+1:]...)

	go m.SaveConfig()
	return true
}

func (m *Manager) findServer(id string) *Server {
	for i := range m.Config.Servers {
		if m.Config.Servers[i].ID == id {
			return &m.Config.Servers[i]
		}
	}
	return nil
}

// getServer locks and returns the server with the given ID, or nil if not found.
func (m *Manager) getServer(id string) *Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.findServer(id)
}

// getClient locks and returns a copy of the client with the given ID.
func (m *Manager) getClient(clientID string) (Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.Config.Clients[clientID]
	return c, ok
}

// getClientInServer locks and returns a copy of the client with the given ID,
// looked up directly within the given server's own Clients list. Unlike
// getClient, this does not rely on the client's (possibly stale/incorrect)
// ServerID field matching serverID - membership in srv.Clients is the
// authoritative relationship.
func (m *Manager) getClientInServer(serverID, clientID string) (Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv := m.findServer(serverID)
	if srv == nil {
		return Client{}, false
	}
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			return srv.Clients[i], true
		}
	}
	return Client{}, false
}

// clientCount locks and returns the total number of clients.
func (m *Manager) clientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.Config.Clients)
}

// copyServers returns a shallow copy of the current server list.
func (m *Manager) copyServers() []Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	servers := make([]Server, len(m.Config.Servers))
	copy(servers, m.Config.Servers)
	return servers
}

// addServer locks and appends a new server to the config.
func (m *Manager) addServer(srv Server) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Config.Servers = append(m.Config.Servers, srv)
}

// setServerStatus locks and updates the status field of a server.
func (m *Manager) setServerStatus(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if srv := m.findServer(id); srv != nil {
		srv.Status = status
	}
}

// setServersPublicIP locks and updates the public IP on every server.
func (m *Manager) setServersPublicIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Config.Servers {
		m.Config.Servers[i].PublicIP = ip
	}
}

// StartServer brings up a WireGuard interface.
func (m *Manager) StartServer(serverID string) bool {
	srv := m.getServer(serverID)
	if srv == nil {
		return false
	}

	if _, err := execCommand(fmt.Sprintf("/usr/bin/awg-quick up %s", srv.Interface)); err != nil {
		fmt.Printf("Failed to start server %s: %v\n", srv.Name, err)
		return false
	}

	m.setupIPTables(srv.Interface, srv.Subnet)
	m.setServerStatus(serverID, "running")
	m.SaveConfig()

	fmt.Printf("Server %s started\n", srv.Name)
	if m.hub != nil {
		go func() {
			time.Sleep(2 * time.Second)
			m.hub.BroadcastServerStatus(serverID, "running")
		}()
	}
	return true
}

// StopServer tears down a WireGuard interface.
func (m *Manager) StopServer(serverID string) bool {
	srv := m.getServer(serverID)
	if srv == nil {
		return false
	}

	m.cleanupIPTables(srv.Interface, srv.Subnet)

	if _, err := execCommand(fmt.Sprintf("/usr/bin/awg-quick down %s", srv.Interface)); err != nil {
		fmt.Printf("Failed to stop server %s: %v\n", srv.Name, err)
		return false
	}

	m.setServerStatus(serverID, "stopped")
	m.SaveConfig()

	fmt.Printf("Server %s stopped\n", srv.Name)
	if m.hub != nil {
		go func() {
			time.Sleep(2 * time.Second)
			m.hub.BroadcastServerStatus(serverID, "stopped")
		}()
	}
	return true
}

// GetServerStatus checks the real interface state.
func (m *Manager) GetServerStatus(serverID string) string {
	srv := m.getServer(serverID)
	if srv == nil {
		return "not_found"
	}
	return m.serverStatusNoLock(srv)
}

// serverStatusNoLock checks the live status of a server without acquiring the mutex.
// Caller must ensure the Server pointer remains valid for the duration of the call.
func (m *Manager) serverStatusNoLock(srv *Server) string {
	result, err := execCommand(fmt.Sprintf("ip link show %s", srv.Interface))
	if err != nil {
		return "stopped"
	}
	if strings.Contains(result, "state UNKNOWN") || strings.Contains(result, srv.Interface) {
		return "running"
	}
	return "stopped"
}

// AddClient adds a WireGuard peer to a server.
func (m *Manager) AddClient(serverID, clientName string, applyI bool, iSettings map[string]string, allowedIPs string) (*Client, string, error) {
	client, ifaceName, isRunning, err := m.addClientLocked(serverID, clientName, applyI, iSettings, allowedIPs)
	if err != nil {
		return nil, "", err
	}

	m.SaveConfig()
	if isRunning {
		m.applyLiveConfig(ifaceName)
	}

	configContent := m.GenerateClientConfig(serverID, client, true)
	fmt.Printf("Client %s added with AllowedIPs: %s\n", clientName, client.AllowedIPs)
	return client, configContent, nil
}

// addClientLocked builds the client, appends it to the server's peer config
// file and to the in-memory config, all under a single write lock.
func (m *Manager) addClientLocked(serverID, clientName string, applyI bool, iSettings map[string]string, allowedIPs string) (client *Client, ifaceName string, isRunning bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil, "", false, fmt.Errorf("server not found")
	}

	clientID := uuid.New().String()[:6]

	privKey, pubKey, genErr := m.generateWireguardKeys()
	if genErr != nil {
		privKey = randomBase64Key()
		pubKey = randomBase64Key()
	}
	psk := m.generatePresharedKey()

	clientIP := m.getNewClientIP(srv)
	if clientIP == "" {
		return nil, "", false, fmt.Errorf("subnet is full")
	}

	serverPeerAllowedIPs := clientIP + "/32"
	clientAllowedIPs := strings.TrimSpace(allowedIPs)
	if clientAllowedIPs == "" {
		clientAllowedIPs = "0.0.0.0/0, ::/0"
	}

	clientISettings := map[string]string{}
	if applyI {
		defaults := map[string]string{
			"i1": DefaultI1, "i2": DefaultI2,
			"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
		}
		for k, v := range defaults {
			clientISettings[k] = v
		}
		for k, v := range iSettings {
			if v != "" {
				clientISettings[k] = v
			}
		}
	}

	newClient := Client{
		ID:                 clientID,
		Name:               clientName,
		ServerID:           serverID,
		ServerName:         srv.Name,
		Status:             "active",
		CreatedAt:          float64(time.Now().Unix()),
		ClientPrivateKey:   privKey,
		ClientPublicKey:    pubKey,
		PresharedKey:       psk,
		ClientIP:           clientIP,
		ObfuscationEnabled: srv.ObfuscationEnabled,
		ObfuscationParams:  srv.ObfuscationParams,
		ApplyISettings:     applyI,
		ISettings:          clientISettings,
		AllowedIPs:         clientAllowedIPs,
	}

	peerConf := fmt.Sprintf("\n# Client: %s\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s\n",
		clientName, pubKey, psk, serverPeerAllowedIPs)

	f, openErr := os.OpenFile(srv.ConfigPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if openErr != nil {
		return nil, "", false, fmt.Errorf("failed to write client to server config: %w", openErr)
	}
	f.WriteString(peerConf)
	f.Close()

	srv.Clients = append(srv.Clients, newClient)
	m.Config.Clients[clientID] = newClient

	return &newClient, srv.Interface, srv.Status == "running", nil
}

func (m *Manager) getNewClientIP(srv *Server) string {
	if len(srv.UnboundNATIPs) > 0 {
		ip := srv.UnboundNATIPs[0]
		srv.UnboundNATIPs = srv.UnboundNATIPs[1:]
		return ip
	}

	usedIPs := map[string]bool{srv.ServerIP: true}
	for _, c := range srv.Clients {
		usedIPs[c.ClientIP] = true
	}

	parts := strings.SplitN(srv.Subnet, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	_, ipNet, err := net.ParseCIDR(srv.Subnet)
	if err != nil {
		return ""
	}

	// Iterate over host IPs in the subnet
	for ip := cloneIP(ipNet.IP); ipNet.Contains(ip); incrementIP(ip) {
		s := ip.String()
		if s == ipNet.IP.String() {
			continue // network address
		}
		// broadcast: last address
		if isBroadcast(ip, ipNet) {
			continue
		}
		if !usedIPs[s] {
			return s
		}
	}
	return ""
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func isBroadcast(ip net.IP, ipNet *net.IPNet) bool {
	mask := ipNet.Mask
	broadcast := make(net.IP, len(ip))
	for i := range ip {
		broadcast[i] = ipNet.IP[i] | ^mask[i]
	}
	return ip.Equal(broadcast)
}

// DeleteClient removes a peer from a server.
func (m *Manager) DeleteClient(serverID, clientID string) bool {
	srv, clientCopy, isRunning, ifaceName, ok := m.deleteClientLocked(serverID, clientID)
	if !ok {
		return false
	}

	m.rewriteServerConfWithoutClient(srv, &clientCopy)
	m.SaveConfig()

	if isRunning {
		m.applyLiveConfig(ifaceName)
	}

	fmt.Printf("Client %s:%s removed\n", srv.Name, clientCopy.Name)
	return true
}

// deleteClientLocked removes the client from the server's in-memory peer list
// under a single write lock.
func (m *Manager) deleteClientLocked(serverID, clientID string) (srv *Server, clientCopy Client, isRunning bool, ifaceName string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv = m.findServer(serverID)
	if srv == nil {
		return nil, Client{}, false, "", false
	}

	var target *Client
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			target = &srv.Clients[i]
			break
		}
	}
	if target == nil {
		return nil, Client{}, false, "", false
	}

	clientCopy = *target

	newClients := make([]Client, 0, len(srv.Clients)-1)
	for _, c := range srv.Clients {
		if c.ID != clientID {
			newClients = append(newClients, c)
		}
	}
	srv.Clients = newClients
	delete(m.Config.Clients, clientID)
	srv.UnboundNATIPs = append(srv.UnboundNATIPs, clientCopy.ClientIP)

	return srv, clientCopy, srv.Status == "running", srv.Interface, true
}

func (m *Manager) rewriteServerConfWithoutClient(srv *Server, client *Client) {
	data, err := os.ReadFile(srv.ConfigPath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	marker := "# Client: " + client.Name
	var out []string
	skip := false
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == marker {
			skip = true
			continue
		}
		if skip && strings.HasPrefix(stripped, "# Client:") {
			skip = false
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	// Trim trailing blank lines
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	os.WriteFile(srv.ConfigPath, []byte(strings.Join(out, "\n")), 0o600)
}

// GenerateClientConfig produces a WireGuard client .conf string.
func (m *Manager) GenerateClientConfig(serverID string, client *Client, includeComments bool) string {
	srv := m.getServer(serverID)
	if srv == nil {
		return ""
	}

	endpoint := srv.Endpoint
	if endpoint == "" {
		endpoint = srv.PublicIP
	}
	if endpoint == "" {
		endpoint = m.PublicIP
	}

	var sb strings.Builder

	if includeComments {
		fmt.Fprintf(&sb, "# AmneziaWG Client Configuration\n")
		fmt.Fprintf(&sb, "# Server: %s\n", srv.Name)
		fmt.Fprintf(&sb, "# Client: %s\n", client.Name)
		fmt.Fprintf(&sb, "# Generated: %s\n", time.Unix(int64(client.CreatedAt), 0).UTC().String())
		fmt.Fprintf(&sb, "# Server Endpoint: %s:%d\n", endpoint, srv.Port)
	}

	fmt.Fprintf(&sb, "[Interface]\n")
	fmt.Fprintf(&sb, "PrivateKey = %s\n", client.ClientPrivateKey)
	fmt.Fprintf(&sb, "Address = %s/32\n", client.ClientIP)
	fmt.Fprintf(&sb, "DNS = %s\n", strings.Join(srv.DNS, ", "))
	fmt.Fprintf(&sb, "MTU = %d\n", srv.MTU)

	if client.ObfuscationEnabled && client.ObfuscationParams != nil {
		p := client.ObfuscationParams
		fmt.Fprintf(&sb, "Jc = %d\n", p.Jc)
		fmt.Fprintf(&sb, "Jmin = %d\n", p.Jmin)
		fmt.Fprintf(&sb, "Jmax = %d\n", p.Jmax)
		fmt.Fprintf(&sb, "S1 = %d\n", p.S1)
		fmt.Fprintf(&sb, "S2 = %d\n", p.S2)
		fmt.Fprintf(&sb, "S3 = %d\n", p.S3)
		fmt.Fprintf(&sb, "S4 = %d\n", p.S4)
		fmt.Fprintf(&sb, "H1 = %d\n", p.H1)
		fmt.Fprintf(&sb, "H2 = %d\n", p.H2)
		fmt.Fprintf(&sb, "H3 = %d\n", p.H3)
		fmt.Fprintf(&sb, "H4 = %d\n", p.H4)
		if p.HeaderProtectionKey != "" {
			fmt.Fprintf(&sb, "HeaderProtectionKey = %s\n", p.HeaderProtectionKey)
		}
		writeIfSet(&sb, "ContentPaddingAddition", p.ContentPaddingAddition)
		writeIfSet(&sb, "RekeyAfterTime", p.RekeyAfterTime)
		writeIfSet(&sb, "RekeyTimeout", p.RekeyTimeout)
		writeIfSet(&sb, "RejectAfterTime", p.RejectAfterTime)
		writeIfSet(&sb, "KeepaliveTimeout", p.KeepaliveTimeout)
		writeIfSet(&sb, "MaxHandshakeAttempts", p.MaxHandshakeAttempts)
		writeAwgBoolIfOn(&sb, "RandomTrailers", p.RandomTrailers)
		writeAwgBoolIfOn(&sb, "DisableCookies", p.DisableCookies)
	}

	if client.ApplyISettings {
		if i1 := client.ISettings["i1"]; i1 != "" {
			for n := 1; n <= 5; n++ {
				key := fmt.Sprintf("i%d", n)
				if v := client.ISettings[key]; v != "" {
					fmt.Fprintf(&sb, "I%d = %s\n", n, v)
				}
			}
		}
	}

	allowedIPs := client.AllowedIPs
	if allowedIPs == "" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}

	fmt.Fprintf(&sb, "\n[Peer]\n")
	fmt.Fprintf(&sb, "PublicKey = %s\n", srv.ServerPublicKey)
	fmt.Fprintf(&sb, "PresharedKey = %s\n", client.PresharedKey)
	fmt.Fprintf(&sb, "Endpoint = %s:%d\n", endpoint, srv.Port)
	fmt.Fprintf(&sb, "AllowedIPs = %s\n", allowedIPs)
	persistentKeepalive := "25"
	if client.ObfuscationParams != nil && client.ObfuscationParams.PersistentKeepalive != "" {
		persistentKeepalive = client.ObfuscationParams.PersistentKeepalive
	}
	fmt.Fprintf(&sb, "PersistentKeepalive = %s\n", persistentKeepalive)

	return sb.String()
}

// GenerateAmneziaVpnURL builds a "vpn://" link in AmneziaVPN's own native
// config format (base64 of the same JSON its app exports/imports).
//
// The official amnezia-client app's raw ".conf" importer
// (ImportController::extractWireGuardConfig) always tags an imported AWG
// config with the container string "amnezia-awg", which resolves to the
// legacy DockerContainer::Awg (pre-3.0, no header protection) rather than
// the current DockerContainer::Awg2 ("amnezia-awg2") - regardless of which
// fields the .conf actually contains. That's why a client importing our
// plain .conf shows the server as "AmneziaWG 2.0" and can fail to connect.
// Emitting the native JSON directly with container="amnezia-awg2" and
// protocol_version="3.1" sidesteps that importer entirely.
func (m *Manager) GenerateAmneziaVpnURL(serverID string, client *Client) (string, error) {
	srv := m.getServer(serverID)
	if srv == nil {
		return "", fmt.Errorf("server not found")
	}

	endpoint := srv.Endpoint
	if endpoint == "" {
		endpoint = srv.PublicIP
	}
	if endpoint == "" {
		endpoint = m.PublicIP
	}

	subnetCidr := "24"
	if parts := strings.SplitN(srv.Subnet, "/", 2); len(parts) > 1 {
		subnetCidr = parts[1]
	}

	allowedIPs := client.AllowedIPs
	if allowedIPs == "" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}
	var allowedIPList []string
	for _, ip := range strings.Split(allowedIPs, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			allowedIPList = append(allowedIPList, ip)
		}
	}

	awgObj := map[string]interface{}{
		"port":            fmt.Sprintf("%d", srv.Port),
		"transport_proto": "udp",
		// The app compares this against its own protocols::awg::awgV3
		// constant, which is the literal string "3.1" in every release that
		// knows about AmneziaWG 3 (5.0.1.5 onwards; no shipped client ever
		// used a bare "3"). A mismatch makes the app render the server with
		// the pre-3.0 settings page and flag it as an outdated container.
		"protocol_version":   awgProtocolVersion,
		"subnet_address":     srv.ServerIP,
		"subnet_cidr":        subnetCidr,
		"isThirdPartyConfig": true,
	}

	persistentKeepalive := "25"
	clientObj := map[string]interface{}{
		"hostName":        endpoint,
		"port":            srv.Port,
		"client_ip":       client.ClientIP,
		"client_priv_key": client.ClientPrivateKey,
		"client_pub_key":  client.ClientPublicKey,
		"server_pub_key":  srv.ServerPublicKey,
		"psk_key":         client.PresharedKey,
		"clientId":        client.ClientPublicKey,
		"allowed_ips":     allowedIPList,
		"mtu":             fmt.Sprintf("%d", srv.MTU),
	}

	if client.ObfuscationEnabled && client.ObfuscationParams != nil {
		p := client.ObfuscationParams
		for k, v := range map[string]string{
			"Jc": fmt.Sprintf("%d", p.Jc), "Jmin": fmt.Sprintf("%d", p.Jmin), "Jmax": fmt.Sprintf("%d", p.Jmax),
			"S1": fmt.Sprintf("%d", p.S1), "S2": fmt.Sprintf("%d", p.S2),
			"S3": fmt.Sprintf("%d", p.S3), "S4": fmt.Sprintf("%d", p.S4),
			"H1": fmt.Sprintf("%d", p.H1), "H2": fmt.Sprintf("%d", p.H2),
			"H3": fmt.Sprintf("%d", p.H3), "H4": fmt.Sprintf("%d", p.H4),
		} {
			awgObj[k] = v
			clientObj[k] = v
		}
		if p.HeaderProtectionKey != "" {
			awgObj["HeaderProtectionKey"] = p.HeaderProtectionKey
			clientObj["HeaderProtectionKey"] = p.HeaderProtectionKey
		}
		for k, v := range map[string]string{
			"ContentPaddingAddition": p.ContentPaddingAddition,
			"RekeyAfterTime":         p.RekeyAfterTime,
			"RekeyTimeout":           p.RekeyTimeout,
			"RejectAfterTime":        p.RejectAfterTime,
			"KeepaliveTimeout":       p.KeepaliveTimeout,
			"MaxHandshakeAttempts":   p.MaxHandshakeAttempts,
			// The app stores these as the strings "on"/"off" (awgBoolOn /
			// awgBoolOff) and treats an absent key as off, so only the "on"
			// case needs to travel.
			"RandomTrailers": awgBoolOrEmpty(p.RandomTrailers),
			"DisableCookies": awgBoolOrEmpty(p.DisableCookies),
		} {
			if v != "" {
				awgObj[k] = v
				clientObj[k] = v
			}
		}
		if p.PersistentKeepalive != "" {
			persistentKeepalive = p.PersistentKeepalive
		}
	}
	clientObj["persistent_keep_alive"] = persistentKeepalive

	if client.ApplyISettings {
		for n := 1; n <= 5; n++ {
			if v := client.ISettings[fmt.Sprintf("i%d", n)]; v != "" {
				iKey := fmt.Sprintf("I%d", n)
				awgObj[iKey] = v
				clientObj[iKey] = v
			}
		}
	}

	clientJSON, err := json.Marshal(clientObj)
	if err != nil {
		return "", err
	}
	awgObj["last_config"] = string(clientJSON)

	root := map[string]interface{}{
		"containers": []interface{}{
			map[string]interface{}{
				"container": "amnezia-awg2",
				"awg":       awgObj,
			},
		},
		"defaultContainer": "amnezia-awg2",
		"description":      fmt.Sprintf("%s - %s", srv.Name, client.Name),
		"hostName":         endpoint,
	}
	if len(srv.DNS) > 0 {
		root["dns1"] = srv.DNS[0]
		if len(srv.DNS) > 1 {
			root["dns2"] = srv.DNS[1]
		}
	}

	rootJSON, err := json.Marshal(root)
	if err != nil {
		return "", err
	}

	return "vpn://" + base64.RawURLEncoding.EncodeToString(rootJSON), nil
}

// UpdateClientAllowedIPs changes the AllowedIPs field for a client.
func (m *Manager) UpdateClientAllowedIPs(serverID, clientID, allowedIPs string) (*Client, string, error) {
	clientCopy, err := m.updateClientAllowedIPsLocked(serverID, clientID, allowedIPs)
	if err != nil {
		return nil, "", err
	}

	go m.SaveConfig()
	cfg := m.GenerateClientConfig(serverID, clientCopy, true)
	return clientCopy, cfg, nil
}

// updateClientAllowedIPsLocked updates the client's AllowedIPs under a
// single write lock and returns a copy of the updated client.
func (m *Manager) updateClientAllowedIPsLocked(serverID, clientID, allowedIPs string) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil, fmt.Errorf("server not found")
	}
	var client *Client
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			client = &srv.Clients[i]
			break
		}
	}
	if client == nil {
		return nil, fmt.Errorf("client not found")
	}

	if strings.TrimSpace(allowedIPs) == "" {
		allowedIPs = "0.0.0.0/0, ::/0"
	}
	client.AllowedIPs = strings.TrimSpace(allowedIPs)
	if gc, ok := m.Config.Clients[clientID]; ok {
		gc.AllowedIPs = client.AllowedIPs
		m.Config.Clients[clientID] = gc
	}

	clientCopy := *client
	return &clientCopy, nil
}

// UpdateClientISettings updates the I1-I5 settings for a client.
func (m *Manager) UpdateClientISettings(serverID, clientID string, applyI *bool, iSettings map[string]string) (*Client, string, error) {
	clientCopy, err := m.updateClientISettingsLocked(serverID, clientID, applyI, iSettings)
	if err != nil {
		return nil, "", err
	}

	go m.SaveConfig()
	cfg := m.GenerateClientConfig(serverID, clientCopy, true)
	return clientCopy, cfg, nil
}

// updateClientISettingsLocked updates the client's I1-I5 settings under a
// single write lock and returns a copy of the updated client.
func (m *Manager) updateClientISettingsLocked(serverID, clientID string, applyI *bool, iSettings map[string]string) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil, fmt.Errorf("server not found")
	}
	var client *Client
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			client = &srv.Clients[i]
			break
		}
	}
	if client == nil {
		return nil, fmt.Errorf("client not found")
	}

	if applyI != nil {
		client.ApplyISettings = *applyI
	}

	if iSettings != nil {
		if client.ApplyISettings {
			newSettings := map[string]string{
				"i1": DefaultI1, "i2": DefaultI2,
				"i3": DefaultI3, "i4": DefaultI4, "i5": DefaultI5,
			}
			for k, v := range client.ISettings {
				newSettings[k] = v
			}
			for k, v := range iSettings {
				if v != "" {
					newSettings[k] = v
				}
			}
			client.ISettings = newSettings
		} else {
			client.ISettings = map[string]string{}
		}
	}

	if gc, ok := m.Config.Clients[clientID]; ok {
		gc.ApplyISettings = client.ApplyISettings
		gc.ISettings = client.ISettings
		m.Config.Clients[clientID] = gc
	}

	clientCopy := *client
	return &clientCopy, nil
}

// SuspendClient removes the client peer block from the active server config.
func (m *Manager) SuspendClient(serverID, clientID string) (bool, string) {
	ok, msg, isRunning, ifaceName := m.suspendClientLocked(serverID, clientID)
	if !ok {
		return false, msg
	}

	m.SaveConfig()
	if isRunning {
		m.applyLiveConfig(ifaceName)
	}
	return true, msg
}

// suspendClientLocked moves the client's peer block out of the live server
// config and marks it suspended, all under a single write lock.
func (m *Manager) suspendClientLocked(serverID, clientID string) (ok bool, msg string, isRunning bool, ifaceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return false, "server not found", false, ""
	}
	var client *Client
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			client = &srv.Clients[i]
			break
		}
	}
	if client == nil {
		return false, "client not found", false, ""
	}

	// Save client peer block to suspended dir
	suspendedDir := filepath.Join(WireguardConfigDir, "suspended")
	os.MkdirAll(suspendedDir, 0o755)

	data, _ := os.ReadFile(srv.ConfigPath)
	content := string(data)
	clientMarker := "# Client: " + client.Name
	lines := strings.Split(content, "\n")
	var peerBlock []string
	inPeer := false
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == clientMarker {
			inPeer = true
			peerBlock = append(peerBlock, line)
		} else if inPeer && stripped == "" && len(peerBlock) > 0 {
			peerBlock = append(peerBlock, line)
			break
		} else if inPeer {
			peerBlock = append(peerBlock, line)
		}
	}
	if len(peerBlock) > 0 {
		suspendedPath := filepath.Join(suspendedDir, clientID+".conf")
		os.WriteFile(suspendedPath, []byte(strings.Join(peerBlock, "\n")), 0o600)
	}

	clientCopy := *client
	m.rewriteServerConfWithoutClient(srv, &clientCopy)

	client.Status = "suspended"
	if gc, ok := m.Config.Clients[clientID]; ok {
		gc.Status = "suspended"
		m.Config.Clients[clientID] = gc
	}

	return true, "client suspended", srv.Status == "running", srv.Interface
}

// ActivateClient restores a suspended client peer block.
func (m *Manager) ActivateClient(serverID, clientID string) (bool, string) {
	ok, msg, isRunning, ifaceName := m.activateClientLocked(serverID, clientID)
	if !ok {
		return false, msg
	}

	m.SaveConfig()
	if isRunning {
		m.applyLiveConfig(ifaceName)
	}
	return true, msg
}

// activateClientLocked restores the client's peer block into the live server
// config and marks it active, all under a single write lock.
func (m *Manager) activateClientLocked(serverID, clientID string) (ok bool, msg string, isRunning bool, ifaceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return false, "server not found", false, ""
	}
	var client *Client
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			client = &srv.Clients[i]
			break
		}
	}
	if client == nil {
		return false, "client not found", false, ""
	}
	if client.Status != "suspended" {
		return false, "client is not suspended", false, ""
	}

	suspendedPath := filepath.Join(WireguardConfigDir, "suspended", clientID+".conf")
	suspended, err := os.ReadFile(suspendedPath)
	if err != nil {
		return false, "suspended config not found", false, ""
	}

	f, err := os.OpenFile(srv.ConfigPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false, "failed to rewrite server config", false, ""
	}
	fmt.Fprintf(f, "\n%s", string(suspended))
	f.Close()
	os.Remove(suspendedPath)

	client.Status = "active"
	if gc, ok := m.Config.Clients[clientID]; ok {
		gc.Status = "active"
		m.Config.Clients[clientID] = gc
	}

	return true, "client activated", srv.Status == "running", srv.Interface
}

// UpdateClientSuspendTime sets or clears the scheduled auto-suspend time.
func (m *Manager) UpdateClientSuspendTime(serverID, clientID string, suspendAt *float64) (*Client, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	srv := m.findServer(serverID)
	if srv == nil {
		return nil, "", fmt.Errorf("server not found")
	}
	var client *Client
	for i := range srv.Clients {
		if srv.Clients[i].ID == clientID {
			client = &srv.Clients[i]
			break
		}
	}
	if client == nil {
		return nil, "", fmt.Errorf("client not found")
	}

	client.SuspendAt = suspendAt
	if gc, ok := m.Config.Clients[clientID]; ok {
		gc.SuspendAt = suspendAt
		m.Config.Clients[clientID] = gc
	}

	clientCopy := *client
	go m.SaveConfig()
	return &clientCopy, "suspension time updated", nil
}

// pendingSuspension identifies a client that has reached its scheduled
// auto-suspend time.
type pendingSuspension struct {
	serverID, clientID string
}

func (m *Manager) startSuspensionChecker() {
	ticker := time.NewTicker(time.Duration(m.SuspendUpdateInterval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := float64(time.Now().Unix())
		for _, it := range m.findClientsPastSuspendTime(now) {
			m.SuspendClient(it.serverID, it.clientID)
			fmt.Printf("Auto-suspended client %s at %s\n", it.clientID, time.Now().Format(time.RFC1123))
		}
	}
}

// findClientsPastSuspendTime locks and returns all active clients whose
// scheduled suspend time has passed.
func (m *Manager) findClientsPastSuspendTime(now float64) []pendingSuspension {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []pendingSuspension
	for _, srv := range m.Config.Servers {
		for _, c := range srv.Clients {
			if c.SuspendAt != nil && c.Status == "active" && now >= *c.SuspendAt {
				items = append(items, pendingSuspension{srv.ID, c.ID})
			}
		}
	}
	return items
}

// applyLiveConfig syncs the running WireGuard interface.
func (m *Manager) applyLiveConfig(iface string) bool {
	cmd := fmt.Sprintf("bash -c 'awg syncconf %s <(awg-quick strip %s)'", iface, iface)
	if _, err := execCommand(cmd); err != nil {
		fmt.Printf("Failed to apply live config to %s: %v\n", iface, err)
		return false
	}
	fmt.Printf("Live config applied to %s\n", iface)
	return true
}

func (m *Manager) setupIPTables(iface, subnet string) {
	script := "/app/scripts/setup_iptables.sh"
	if _, err := os.Stat(script); err == nil {
		execCommand(fmt.Sprintf("%s %s %s", script, iface, subnet))
	}
}

func (m *Manager) cleanupIPTables(iface, subnet string) {
	script := "/app/scripts/cleanup_iptables.sh"
	if _, err := os.Stat(script); err == nil {
		execCommand(fmt.Sprintf("%s %s %s", script, iface, subnet))
	}
}

// GetPeerTrafficForServer parses `awg show` output.
func (m *Manager) GetPeerTrafficForServer(serverID string) map[string]ClientTraffic {
	srv := m.getServer(serverID)
	if srv == nil {
		return nil
	}

	output, err := execCommand(fmt.Sprintf("/usr/bin/awg show %s", srv.Interface))
	if err != nil || output == "" {
		return nil
	}

	peerData := map[string]map[string]string{}
	currentPeer := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "peer:") {
			currentPeer = strings.TrimSpace(strings.TrimPrefix(line, "peer:"))
			peerData[currentPeer] = map[string]string{
				"received": "0 B", "sent": "0 B",
				"last_handshake": "Never", "endpoint": "",
			}
		} else if strings.HasPrefix(line, "transfer:") && currentPeer != "" {
			parts := strings.SplitN(strings.TrimPrefix(line, "transfer:"), ",", 2)
			if len(parts) == 2 {
				peerData[currentPeer]["received"] = strings.TrimSpace(parts[0])
				// "X sent" -> strip " sent"
				sent := strings.TrimSpace(parts[1])
				sent = strings.TrimSuffix(sent, " sent")
				peerData[currentPeer]["sent"] = strings.TrimSpace(sent)
			}
		} else if strings.HasPrefix(line, "endpoint:") && currentPeer != "" {
			peerData[currentPeer]["endpoint"] = strings.TrimSpace(strings.TrimPrefix(line, "endpoint:"))
		} else if strings.HasPrefix(line, "latest handshake:") && currentPeer != "" {
			peerData[currentPeer]["last_handshake"] = strings.TrimSpace(strings.TrimPrefix(line, "latest handshake:"))
		}
	}

	return m.buildClientTraffic(serverID, peerData)
}

// buildClientTraffic locks and joins parsed `awg show` peer data with the
// known clients for a server.
func (m *Manager) buildClientTraffic(serverID string, peerData map[string]map[string]string) map[string]ClientTraffic {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := map[string]ClientTraffic{}
	for cid, c := range m.Config.Clients {
		if c.ServerID != serverID {
			continue
		}
		if pd, ok := peerData[c.ClientPublicKey]; ok {
			result[cid] = ClientTraffic{
				Received:      pd["received"],
				Sent:          pd["sent"],
				LastHandshake: pd["last_handshake"],
				Endpoint:      pd["endpoint"],
			}
		} else {
			result[cid] = ClientTraffic{Received: "0 B", Sent: "0 B", LastHandshake: "Never"}
		}
	}
	return result
}

// GetServerInterfaceTraffic parses `ifconfig` for RX/TX on an interface.
func (m *Manager) GetServerInterfaceTraffic(iface string) map[string]string {
	output, err := execCommand(fmt.Sprintf("ifconfig %s", iface))
	if err != nil || output == "" {
		return nil
	}
	rxRe := regexp.MustCompile(`RX bytes:\d+\s+\(([^)]+)\)`)
	txRe := regexp.MustCompile(`TX bytes:\d+\s+\(([^)]+)\)`)
	rx, tx := "0 B", "0 B"
	if m := rxRe.FindStringSubmatch(output); len(m) > 1 {
		rx = m[1]
	}
	if m := txRe.FindStringSubmatch(output); len(m) > 1 {
		tx = m[1]
	}
	return map[string]string{"rx": rx, "tx": tx}
}

// GetAllServersTraffic returns interface traffic for every server.
func (m *Manager) GetAllServersTraffic() map[string]map[string]string {
	servers := m.copyServers()

	result := map[string]map[string]string{}
	for _, srv := range servers {
		if t := m.GetServerInterfaceTraffic(srv.Interface); t != nil {
			result[srv.ID] = t
		}
	}
	return result
}

// RefreshPublicIP re-detects and updates the stored public IP.
func (m *Manager) RefreshPublicIP() string {
	ip := m.detectPublicIP()
	m.PublicIP = ip
	m.setServersPublicIP(ip)
	m.SaveConfig()
	return ip
}

// GetClientConfigs returns clients, optionally filtered by server.
func (m *Manager) GetClientConfigs(serverID string) []Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if serverID != "" {
		srv := m.findServer(serverID)
		if srv == nil {
			return []Client{}
		}
		return srv.Clients
	}
	clients := make([]Client, 0, len(m.Config.Clients))
	for _, c := range m.Config.Clients {
		if c.Status == "" {
			c.Status = "active"
		}
		if c.ISettings == nil {
			c.ISettings = map[string]string{}
		}
		clients = append(clients, c)
	}
	return clients
}

// GetServersWithStatus returns all servers with current live status.
func (m *Manager) GetServersWithStatus() []Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Config.Servers {
		m.Config.Servers[i].Status = m.serverStatusNoLock(&m.Config.Servers[i])
		if m.Config.Servers[i].MTU == 0 {
			m.Config.Servers[i].MTU = 1420
		}
	}
	go m.SaveConfig()
	result := make([]Server, len(m.Config.Servers))
	copy(result, m.Config.Servers)
	return result
}
