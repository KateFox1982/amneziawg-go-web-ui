package internal

import (
	"os"
	"strings"
	"testing"
)

func TestMigrateLegacyClientMapKeepsTheNewerCopy(t *testing.T) {
	cfg := &AppConfig{
		Servers: []Server{{ID: "s1", Clients: []Client{
			{ID: "c1", Name: "stale"},
			{ID: "c2", Name: "only-in-server"},
		}}},
		Clients: map[string]Client{"c1": {ID: "c1", Name: "current"}},
	}
	migrateLegacyClientMap(cfg)

	if got := cfg.Clients["c1"].Name; got != "current" {
		t.Errorf("existing map entry must win, got %q", got)
	}
	if got := cfg.Clients["c2"].Name; got != "only-in-server" {
		t.Errorf("client missing from the map should be copied in, got %q", got)
	}
}

// serverConf is the shape CreateServer writes, with one peer appended by
// AddClient - the layout ensureAwgInterfaceFlags has to edit in place.
const serverConf = `[Interface]
PrivateKey = PRIV
Address = 10.0.1.1/24
ListenPort = 51834
MTU = 1280
S1 = 50

# Client: alice
[Peer]
PublicKey = APUB
AllowedIPs = 10.0.1.2/32
`

func writeConf(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/wg-t.conf"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnsureAwgInterfaceFlagsInsertsBeforePeerComment(t *testing.T) {
	path := writeConf(t, serverConf)
	if err := ensureAwgInterfaceFlags(path, "RandomTrailers", "DisableCookies"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	lines := strings.Split(string(got), "\n")

	iface, comment := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case "RandomTrailers = on":
			iface = i
		case "# Client: alice":
			comment = i
		}
	}
	if iface < 0 || comment < 0 || iface > comment {
		t.Fatalf("RandomTrailers must land inside [Interface], before the peer comment:\n%s", got)
	}
	// rewriteServerConfWithoutClient deletes from "# Client:" onward, so the
	// flags must survive removing that client.
	m := &Manager{Config: &AppConfig{}}
	m.rewriteServerConfWithoutClient(&Server{ConfigPath: path}, &Client{Name: "alice"})
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "RandomTrailers = on") ||
		!strings.Contains(string(after), "DisableCookies = on") {
		t.Fatalf("flags lost when the client was removed:\n%s", after)
	}
	if strings.Contains(string(after), "APUB") {
		t.Fatalf("peer block should be gone:\n%s", after)
	}
}

func TestEnsureAwgInterfaceFlagsIsIdempotentAndKeepsExplicitOff(t *testing.T) {
	path := writeConf(t, "[Interface]\nPrivateKey = PRIV\nRandomTrailers = off\n")
	for i := 0; i < 3; i++ {
		if err := ensureAwgInterfaceFlags(path, "RandomTrailers", "DisableCookies"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	if strings.Count(string(got), "RandomTrailers") != 1 {
		t.Fatalf("existing RandomTrailers must be left alone, not duplicated:\n%s", got)
	}
	if !strings.Contains(string(got), "RandomTrailers = off") {
		t.Fatalf("explicit off was overwritten:\n%s", got)
	}
	if strings.Count(string(got), "DisableCookies = on") != 1 {
		t.Fatalf("DisableCookies should be added exactly once:\n%s", got)
	}
}

func TestEnsureAwgInterfaceFlagsRejectsConfWithoutInterface(t *testing.T) {
	path := writeConf(t, "# nothing here\n")
	if err := ensureAwgInterfaceFlags(path, "RandomTrailers"); err == nil {
		t.Fatal("expected an error for a conf with no [Interface] section")
	}
}
