# CHANGELOG

## Version 2.0
Initial release

## Version 3.0
- Added AmneziaWG 3.0 (header protection) support: `HeaderProtectionKey`
  obfuscation parameter, enforcement of the S1-S4 ≥ 12 requirement, and
  optional client-side timing knobs (`ContentPaddingAddition`,
  `RekeyAfterTime`, `RekeyTimeout`, `RejectAfterTime`, `KeepaliveTimeout`,
  `MaxHandshakeAttempts`, `PersistentKeepalive`).
- **Breaking**: removed support for AmneziaWG 1.0/1.5/2.0-only modes and the
  `awg2`/`awg3` request flags. Enabling obfuscation now always uses the full
  AmneziaWG 3.0 parameter set (header protection is mandatory, S1-S4 always
  ≥ 12). Existing already-running servers are unaffected — this only
  changes newly created servers.
- Added an AmneziaVPN native config export (`vpn://…` link, new
  `GET /api/servers/:id/clients/:clientId/link` endpoint, third view
  in the client QR modal). The official `amnezia-client` app's plain-`.conf`
  importer always tags an imported AWG config as its legacy `amnezia-awg`
  container (shown as "AmneziaWG 2.0", no header protection) instead of the
  current `amnezia-awg2` — this native link carries the
  `container`/`protocol_version` fields needed for the app to recognize the
  server as AmneziaWG 3.0.

## Version 3.1
- Added AmneziaWG 3.1 support: the two new `[Interface]` switches
  `RandomTrailers` (random-length tail on every packet, so handshakes lose
  their fixed on-the-wire size) and `DisableCookies` (never send cookie
  replies, skip under-load MAC2 verification). Both are on by default for
  newly created servers, exposed as checkboxes in the create-server form,
  and written into the server `.conf`, every client `.conf` and the
  `vpn://` link from the same stored values. `RandomTrailers` must match on
  both ends — a receiver without it drops the oversized handshake, silently
  — while `DisableCookies` is purely local. A client older than AmneziaWG
  3.1 (AmneziaVPN < 5.0.1.5) knows neither key, so untick them if such
  clients have to connect. An off switch is written as an absent key
  rather than `= off`, since a pre-3.1 parser rejects an unknown key.
  Existing servers are unaffected — their stored parameters have neither
  flag, and absent means off.
- Added a one-shot migration that turns both 3.1 switches on for servers and
  clients created before this release: it updates the stored parameters and
  each server `.conf`, then stamps `schema_version: 1` into
  `web_config.json` so it runs exactly once and doesn't undo a later
  decision to switch them off. Servers running when it fires are named in
  the log as needing a restart. No keys change. Since `RandomTrailers` must
  match, **client configs already distributed stop connecting until
  re-exported**.
- Fixed the `protocol_version` in the AmneziaVPN native config export: the
  app compares it against its own `awgV3` constant, which is the literal
  string `"3.1"` in every release that knows about AmneziaWG 3 (5.0.1.5
  onwards). The previous `"3"` matched nothing, making the app render the
  server with the pre-3.0 settings page and flag it as an outdated
  container.
- **Pinned the upstream AmneziaWG versions in the `Dockerfile`**
  (`AWG_GO_VERSION=v3.1.20260828`, `AWG_TOOLS_VERSION=v3.1.20260812`)
  instead of cloning `master`, so a rebuild can no longer silently pick up
  an incompatible or broken upstream commit. Override with
  `--build-arg` to build against a different tag.
