package internal

import (
	"fmt"
	"os"
	"strings"
)

// This file holds the one-shot migrations that bring a config written by an
// older release up to what the current code expects, plus the helpers they
// need. They run at startup, before anything reads the config for real:
// migrateLegacyClientMap from loadConfig, migrateExistingServersToAwg31 from
// NewManager.

// migrateLegacyClientMap copies clients that live only in a server's own
// Clients slice into the top-level map, which is where the rest of the code
// looks them up. Needed for configs written before that map existed; a
// client already in the map wins, since it is the newer copy.
func migrateLegacyClientMap(cfg *AppConfig) {
	for i := range cfg.Servers {
		for _, client := range cfg.Servers[i].Clients {
			if _, exists := cfg.Clients[client.ID]; !exists {
				cfg.Clients[client.ID] = client
			}
		}
	}
}

// awgConfigSchemaVersion is the current web_config.json schema revision.
//
//	1 - AmneziaWG 3.1: RandomTrailers/DisableCookies turned on for servers
//	    and clients that predate those fields.
const awgConfigSchemaVersion = 1

// migrateExistingServersToAwg31 switches the two AmneziaWG 3.1 flags on for
// every obfuscated server (and its clients) created before those fields
// existed, so an upgraded deployment matches what a freshly created server
// gets. It runs once - awgConfigSchemaVersion is stamped into the config
// afterwards, so a later decision to turn the flags back off is not undone
// by the next restart.
//
// This is deliberately not silent: RandomTrailers has to agree on both ends,
// so every already-distributed client config stops working until it is
// re-exported with the matching line.
func (m *Manager) migrateExistingServersToAwg31() {
	if m.Config.SchemaVersion >= awgConfigSchemaVersion {
		return
	}

	enable := func(p *ObfuscationParams) bool {
		if p == nil || (p.RandomTrailers && p.DisableCookies) {
			return false
		}
		p.RandomTrailers = true
		p.DisableCookies = true
		return true
	}

	var upgraded []string
	for i := range m.Config.Servers {
		srv := &m.Config.Servers[i]
		if !srv.ObfuscationEnabled || !enable(srv.ObfuscationParams) {
			continue
		}
		// The .conf on disk is what awg-quick actually reads, so the stored
		// parameters alone would only reach the clients and leave the server
		// on the old framing - the exact mismatch that breaks handshakes.
		if err := ensureAwgInterfaceFlags(srv.ConfigPath, "RandomTrailers", "DisableCookies"); err != nil {
			fmt.Printf("AWG 3.1 migration: failed to update %s: %v\n", srv.ConfigPath, err)
		}
		upgraded = append(upgraded, srv.Name)
		if m.serverStatusNoLock(srv) == "running" {
			fmt.Printf("AWG 3.1 migration: server %q is running - restart it to pick up the new flags\n", srv.Name)
		}
	}

	// Clients carry their own copy of the parameters once the config has
	// been through a save/load cycle, and that copy is what every generated
	// .conf and vpn:// link is built from.
	for _, c := range m.Config.Clients {
		if c.ObfuscationEnabled {
			enable(c.ObfuscationParams)
		}
	}
	for i := range m.Config.Servers {
		for j := range m.Config.Servers[i].Clients {
			c := &m.Config.Servers[i].Clients[j]
			if c.ObfuscationEnabled {
				enable(c.ObfuscationParams)
			}
		}
	}

	m.Config.SchemaVersion = awgConfigSchemaVersion
	m.SaveConfig()

	if len(upgraded) > 0 {
		fmt.Printf("AWG 3.1 migration: enabled RandomTrailers/DisableCookies on %d server(s): %s\n",
			len(upgraded), strings.Join(upgraded, ", "))
		fmt.Println("AWG 3.1 migration: RandomTrailers must match on both ends - re-export and redistribute every client config, or already-issued ones will stop connecting")
	}
}

// ensureAwgInterfaceFlags adds "<key> = on" to the [Interface] section of an
// AmneziaWG .conf for each key that isn't already set there, leaving an
// existing value alone. The line is inserted before the first peer block -
// and before the "# Client: <name>" comment that introduces it, since
// rewriteServerConfWithoutClient deletes everything from that comment to the
// next one.
func ensureAwgInterfaceFlags(path string, keys ...string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")

	header := -1
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "[Interface]") {
			header = i
			break
		}
	}
	if header < 0 {
		return fmt.Errorf("no [Interface] section")
	}

	end := len(lines)
	for i := header + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "# Client:") {
			end = i
			break
		}
	}

	var missing []string
	for _, key := range keys {
		found := false
		for _, line := range lines[header+1 : end] {
			field, _, ok := strings.Cut(line, "=")
			if ok && strings.EqualFold(strings.TrimSpace(field), key) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	insert := end
	for insert > header+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}
	added := make([]string, 0, len(missing))
	for _, key := range missing {
		added = append(added, key+" = on")
	}
	out := append([]string{}, lines[:insert]...)
	out = append(out, added...)
	out = append(out, lines[insert:]...)

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600)
}
