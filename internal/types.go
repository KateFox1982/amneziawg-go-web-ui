package internal

const (
	DefaultI1 = "<b 0xc70000000108ce1bf31eec7d93360000449e227e4596ed7f75c4d35ce31880b4133107c822c6355b51f0d7c1bba96d5c210a48aca01885fed0871cfc37d59137d73b506dc013bb4a13c060ca5b04b7ae215af71e37d6e8ff1db235f9fe0c25cb8b492471054a7c8d0d6077d430d07f6e87a8699287f6e69f54263c7334a8e144a29851429bf2e350e519445172d36953e96085110ce1fb641e5efad42c0feb4711ece959b72cc4d6f3c1e83251adb572b921534f6ac4b10927167f41fe50040a75acef62f45bded67c0b45b9d655ce374589cad6f568b8475b2e8921ff98628f86ff2eb5bcce6f3ddb7dc89e37c5b5e78ddc8d93a58896e530b5f9f1448ab3b7a1d1f24a63bf981634f6183a21af310ffa52e9ddf5521561760288669de01a5f2f1a4f922e68d0592026bbe4329b654d4f5d6ace4f6a23b8560b720a5350691c0037b10acfac9726add44e7d3e880ee6f3b0d6429ff33655c297fee786bb5ac032e48d2062cd45e305e6d8d8b82bfbf0fdbc5ec09943d1ad02b0b5868ac4b24bb10255196be883562c35a713002014016b8cc5224768b3d330016cf8ed9300fe6bf39b4b19b3667cddc6e7c7ebe4437a58862606a2a66bd4184b09ab9d2cd3d3faed4d2ab71dd821422a9540c4c5fa2a9b2e6693d411a22854a8e541ed930796521f03a54254074bc4c5bca152a1723260e7d70a24d49720acc544b41359cfc252385bda7de7d05878ac0ea0343c77715e145160e6562161dfe2024846dfda3ce99068817a2418e66e4f37dea40a21251c8a034f83145071d93baadf050ca0f95dc9ce2338fb082d64fbc8faba905cec66e65c0e1f9b003c32c943381282d4ab09bef9b6813ff3ff5118623d2617867e25f0601df583c3ac51bc6303f79e68d8f8de4b8363ec9c7728b3ec5fcd5274edfca2a42f2727aa223c557afb33f5bea4f64aeb252c0150ed734d4d8eccb257824e8e090f65029a3a042a51e5cc8767408ae07d55da8507e4d009ae72c47ddb138df3cab6cc023df2532f88fb5a4c4bd917fafde0f3134be09231c389c70bc55cb95a779615e8e0a76a2b4d943aabfde0e394c985c0cb0376930f92c5b6998ef49ff4a13652b787503f55c4e3d8eebd6e1bc6db3a6d405d8405bd7a8db7cefc64d16e0d105a468f3d33d29e5744a24c4ac43ce0eb1bf6b559aed520b91108cda2de6e2c4f14bc4f4dc58712580e07d217c8cca1aaf7ac04bab3e7b1008b966f1ed4fba3fd93a0a9d3a27127e7aa587fbcc60d548300146bdc126982a58ff5342fc41a43f83a3d2722a26645bc961894e339b953e78ab395ff2fb854247ad06d446cc2944a1aefb90573115dc198f5c1efbc22bc6d7a74e41e666a643d5f85f57fde81b87ceff95353d22ae8bab11684180dd142642894d8dc34e402f802c2fd4a73508ca99124e428d67437c871dd96e506ffc39c0fc401f666b437adca41fd563cbcfd0fa22fbbf8112979c4e677fb533d981745cceed0fe96da6cc0593c430bbb71bcbf924f70b4547b0bb4d41c94a09a9ef1147935a5c75bb2f721fbd24ea6a9f5c9331187490ffa6d4e34e6bb30c2c54a0344724f01088fb2751a486f425362741664efb287bce66c4a544c96fa8b124d3c6b9eaca170c0b530799a6e878a57f402eb0016cf2689d55c76b2a91285e2273763f3afc5bc9398273f5338a06d>"
	DefaultI2 = ""
	DefaultI3 = ""
	DefaultI4 = ""
	DefaultI5 = ""

	ConfigDir          = "/etc/amnezia"
	WireguardConfigDir = "/etc/amnezia/amneziawg"
	ConfigFile         = "/etc/amnezia/web_config.json"
)

// AppConfig is the top-level config stored on disk.
type AppConfig struct {
	// SchemaVersion records which one-shot migrations have already been
	// applied to this file, so they don't re-run on every start and undo a
	// choice the operator made afterwards. A file written before this field
	// existed unmarshals to 0.
	SchemaVersion int               `json:"schema_version"`
	Servers       []Server          `json:"servers"`
	Clients       map[string]Client `json:"clients"`
}

// ObfuscationParams holds AmneziaWG obfuscation parameters.
type ObfuscationParams struct {
	Jc   int `json:"Jc"`
	Jmin int `json:"Jmin"`
	Jmax int `json:"Jmax"`
	S1   int `json:"S1"`
	S2   int `json:"S2"`
	S3   int `json:"S3"`
	S4   int `json:"S4"`
	H1   int `json:"H1"`
	H2   int `json:"H2"`
	H3   int `json:"H3"`
	H4   int `json:"H4"`
	MTU  int `json:"MTU"`
	// HeaderProtectionKey is a 32-byte base64-encoded key used by the
	// AmneziaWG 3.0 header protection mechanism. Generated automatically
	// whenever obfuscation is enabled, and must match byte-for-byte between
	// server and client.
	HeaderProtectionKey string `json:"HeaderProtectionKey,omitempty"`

	// The following are optional AmneziaWG 3.0 "client-side" tuning knobs:
	// each side of the tunnel applies them to its own behavior, so they do
	// NOT need to match between server and client. All accept either a
	// plain integer or an "a-b" range (e.g. "5-10"); empty means "use the
	// engine default".
	ContentPaddingAddition string `json:"ContentPaddingAddition,omitempty"`
	RekeyAfterTime         string `json:"RekeyAfterTime,omitempty"`
	RekeyTimeout           string `json:"RekeyTimeout,omitempty"`
	RejectAfterTime        string `json:"RejectAfterTime,omitempty"`
	KeepaliveTimeout       string `json:"KeepaliveTimeout,omitempty"`
	MaxHandshakeAttempts   string `json:"MaxHandshakeAttempts,omitempty"`
	// PersistentKeepalive overrides the hardcoded "25" written to client
	// [Peer] sections; accepts a plain integer or "a-b" range. Empty means
	// keep the default of 25.
	PersistentKeepalive string `json:"PersistentKeepalive,omitempty"`

	// The following two are AmneziaWG 3.1 additions. Both are written into
	// the server .conf and every client config from the same stored values,
	// which keeps RandomTrailers - the one of the two that has to agree on
	// both ends - in sync without any manual copying.

	// RandomTrailers appends a random number of bytes to every packet, so a
	// handshake no longer has a fixed on-the-wire length. The receiver only
	// tolerates the extra bytes when it has the same flag on - a 3.0-era
	// client (amnezia-client < 5.0.1.5, older amneziawg-android/apple) will
	// silently drop the oversized handshake.
	RandomTrailers bool `json:"RandomTrailers"`

	// DisableCookies stops the interface from ever answering with a cookie
	// reply, and disables under-load MAC2 verification altogether, removing
	// the cookie exchange as a fingerprint. Purely local behavior, but it is
	// written to both sides for consistency.
	DisableCookies bool `json:"DisableCookies"`
}

// Server holds a WireGuard server configuration.
type Server struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Protocol           string             `json:"protocol"`
	Port               int                `json:"port"`
	Status             string             `json:"status"`
	Interface          string             `json:"interface"`
	ConfigPath         string             `json:"config_path"`
	ServerPublicKey    string             `json:"server_public_key"`
	ServerPrivateKey   string             `json:"server_private_key"`
	Subnet             string             `json:"subnet"`
	ServerIP           string             `json:"server_ip"`
	MTU                int                `json:"mtu"`
	PublicIP           string             `json:"public_ip"`
	Endpoint           string             `json:"endpoint"`
	ObfuscationEnabled bool               `json:"obfuscation_enabled"`
	ObfuscationParams  *ObfuscationParams `json:"obfuscation_params"`
	AutoStart          bool               `json:"auto_start"`
	DNS                []string           `json:"dns"`
	Clients            []Client           `json:"clients"`
	UnboundNATIPs      []string           `json:"unbound_nat_ips"`
	CreatedAt          float64            `json:"created_at"`
}

// Client holds a WireGuard client configuration.
type Client struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	ServerID           string             `json:"server_id"`
	ServerName         string             `json:"server_name"`
	Status             string             `json:"status"`
	CreatedAt          float64            `json:"created_at"`
	ClientPrivateKey   string             `json:"client_private_key"`
	ClientPublicKey    string             `json:"client_public_key"`
	PresharedKey       string             `json:"preshared_key"`
	ClientIP           string             `json:"client_ip"`
	ObfuscationEnabled bool               `json:"obfuscation_enabled"`
	ObfuscationParams  *ObfuscationParams `json:"obfuscation_params"`
	ApplyISettings     bool               `json:"apply_i_settings"`
	ISettings          map[string]string  `json:"i_settings"`
	AllowedIPs         string             `json:"allowed_ips"`
	SuspendAt          *float64           `json:"suspend_at,omitempty"`
}

// CreateServerRequest is the payload to create a new server.
type CreateServerRequest struct {
	Name              string             `json:"name"`
	Port              int                `json:"port"`
	Subnet            string             `json:"subnet"`
	MTU               int                `json:"mtu"`
	Endpoint          string             `json:"endpoint"`
	DNS               interface{}        `json:"dns"`
	Obfuscation       *bool              `json:"obfuscation"`
	ObfuscationParams *ObfuscationParams `json:"obfuscation_params"`
	AutoStart         *bool              `json:"auto_start"`
}

// AddClientRequest is the payload to add a new client.
type AddClientRequest struct {
	Name           string            `json:"name"`
	ApplyISettings bool              `json:"apply_i_settings"`
	ISettings      map[string]string `json:"i_settings"`
	AllowedIPs     string            `json:"allowed_ips"`
}

// UpdateAllowedIPsRequest is the payload to update client AllowedIPs.
type UpdateAllowedIPsRequest struct {
	AllowedIPs string `json:"allowed_ips"`
}

// UpdateISettingsRequest is the payload to update client I-settings.
type UpdateISettingsRequest struct {
	ApplyISettings *bool             `json:"apply_i_settings"`
	ISettings      map[string]string `json:"i_settings"`
}

// UpdateSuspendTimeRequest is the payload to set auto-suspend time.
type UpdateSuspendTimeRequest struct {
	SuspendAt *string `json:"suspend_at"` // ISO 8601 or null
}

// ClientTraffic holds traffic statistics for a single client.
type ClientTraffic struct {
	Received      string `json:"received"`
	Sent          string `json:"sent"`
	LastHandshake string `json:"last_handshake"`
	Endpoint      string `json:"endpoint"`
}
