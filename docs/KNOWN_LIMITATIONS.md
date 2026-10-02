# Known limitations — 1.0.0-rc.1

- Clean install only; no import from `blanc-auto`.
- Xray only. The adapter boundary is ready for other cores, but none are implemented.
- Supported nodes are VLESS Reality/TCP and VLESS WebSocket/TLS only.
- Supported subscription formats are plain/base64 URI lists only.
- Xray must use a confdir layout. Single monolithic config files are not supported.
- The adopted base routing fragment must be strict JSON. Other Xray fragments may still use syntax accepted by Xray.
- One controller manages one gateway.
- Managed firewall mode is IPv4-only in RC1. Existing firewall mode is recommended unless the operator has verified ports and interfaces.
- A dead remote VPN server breaks existing sessions. KRM redirects new connections; it cannot migrate remote TCP/UDP state.
- Self-signed TLS requires a browser trust decision unless replaced by an operator certificate.
- Sessions are in memory and require login again after daemon restart.
- UI-proxy mode is provided for Linux/systemd hosts; other desktops can use a normal browser.
- Release binaries are Linux-only.
- Automatic update hosting becomes operational only after release assets and signed manifests are published at the configured URLs.
- This generated RC passed local automated checks and cross-compilation. It has not been installed on the user's physical router from this execution environment.
