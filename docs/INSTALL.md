# Installation

**Version 2.0.1 security:** built with Go 1.27.3, with the fix for [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) (`crypto/x509` compromised-intermediate distrust). Legacy immutable v1.3.3 uses affected Go 1.27.2; its signatures do not patch that toolchain. Follow [offline migration](BREAKING_CHANGES.md#201-keeneticnetcraze-only-router-targets) before upgrading an older generated Keenetic YAML. No asset or historical tag is rewritten.

**Release selection:** this runbook accompanies [v2.0.1](https://github.com/jarymor-ux/kee-route-manager/releases/tag/v2.0.1). Verify that its complete signed asset set is actually published before starting; if missing, stop. Choose one immutable release and matching artifacts. Never install from mutable `main` or `/releases/latest`, and do not substitute the older vulnerable v1.3.3.

This is a short entry point. The authoritative step-by-step runbook, including backups, trust verification, migration and recovery, is [AGENT_INSTALL.md](AGENT_INSTALL.md).

**Scope:** only Keenetic/Netcraze stock firmware and working Entware/Xray. See [hardware catalogue and KN-2410 steps](HARDWARE.md). Version 2.0.1 adds signed MIPS BE binaries; KN-2410/2510 remain experimental and require actual hardware acceptance. A separate PC can run the management UI manually, never KRM Core as a router.

## Prerequisites

- A working Xray installation with valid **strict-JSON** configuration, known inbound/routing tags and access to its actual files. KRM does **not** install or configure Xray, XKeen or Entware for you.
- Authorized SSH/root access, an out-of-band recovery path and a private backup of router/Xray configuration. Hardware support on Keenetic/Netcraze remains experimental; read [known limitations](KNOWN_LIMITATIONS.md).
- Independent health targets and your own supported VLESS subscription(s). Do not commit subscription URLs, provider headers, credentials or device identifiers to Git.

Before installation, run `kee-route-managerctl preflight --config /PRIVATE/config.yaml --mode core` (or `local-ui`) with the signature-verified matching CLI. `--release-dir /PRIVATE/verified-release` also checks the extracted bundle. JSON results and the exit status describe actual gates, not model-name guesses; a failed required gate must stop installation. The Keenetic wrapper runs this preflight automatically. Check actual free `/opt` and staging space; use a private `TMPDIR` on persistent Entware storage rather than filling RAM-backed `/tmp`. For KN-2410, follow the [stock firmware → storage → BE Entware → XKeen/Xray → preflight → signed KRM → client-route procedure](HARDWARE.md#kn-2410-stock-firmware-to-an-accepted-installation).

## Fresh interactive installation

1. Select the signed `bootstrap-keenetic.sh` **asset** from the same fixed release. Verify the source, release inventory and signing-key trust as described in the runbook.
2. Download that specific platform asset to the target host, **without piping the download into a shell**:

   ```sh
   curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh RELEASE_URL
   sh /tmp/krm-bootstrap.sh
   ```

   Replace `RELEASE_URL` with the HTTPS URL of the chosen release's matching bootstrap asset. The bootstrap verifies the signed manifest and package hashes before executing downloaded binaries. Its own initial download is still dependent on your trusted GitHub/TLS path.
3. With a terminal and no `KRM_CONFIG_FILE`, the signed RU/EN wizard builds the validated private controller configuration. Default mode is `local-ui`; set `KRM_MODE=core` to install the controller only. Do not guess routing tags. Use a verified `kee-route-managerctl route-candidates --file PATH` to inspect existing Xray rules.
4. Verify daemon readiness, Xray selection, panel login if enabled, and the actual client route. HTTP 200 alone does not establish VPN health. Preserve configuration/state backups and recovery access.

For generated local UI, the controller API uses TLS on loopback and the UI trusts its installed public certificate. The browser-facing panel also uses HTTPS. A new local wizard installation generates **both** YAML configurations; no second handwritten UI file is needed.

## Prepared / noninteractive configuration

To skip the wizard, prepare a private controller YAML from `configs/keenetic.yaml`, validate the Xray paths/tags and run:

```sh
KRM_MODE=core KRM_CONFIG_FILE=/root/krm-install/config.yaml sh /tmp/krm-bootstrap.sh
```

For prepared `KRM_MODE=local-ui`, supply **both** `KRM_CONFIG_FILE` and `KRM_UI_CONFIG_FILE`, with matching controller API and UI upstream TLS settings. Unlike wizard-generated files, the tracked controller templates use an HTTP **loopback-only** API and the tracked UI templates use `http://127.0.0.1:9443` upstream. Do not change either loopback upstream address to a LAN/WAN listener. The supplied UI templates bind `0.0.0.0:9444` with browser-facing TLS: review or restrict that listener to the trusted private interface before installing.

UI-only on a supported router uses `KRM_MODE=ui` and needs a prepared UI YAML, `KRM_UPSTREAM_CA_FILE` and an authenticated SSH tunnel to the controller; no unchecked upstream certificates. Existing installations are **never overwritten** by the clean installer. Use the runbook's dedicated migration/restore/uninstall process, not the fresh bootstrap over a running installation.

Signed update **checks** can be automatic, but applying a release requires an explicit UI/CLI action. The launcher is maintained separately; remote management UI is updated manually. See [release details](RELEASE.md), [configuration](CONFIGURATION.md) and [limitations](KNOWN_LIMITATIONS.md).
