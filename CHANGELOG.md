# CHANGELOG


## Version 1.0
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
