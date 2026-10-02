# Installation

## Before installation

- Keep a working SSH/serial/recovery path to the gateway.
- Confirm the existing Xray configuration passes `xray run -test`.
- Identify the strict-JSON routing fragment that currently points `redirect`/`tproxy` traffic to `vless-reality` or the configured legacy tag.
- Prepare your own subscription, score and health URLs.
- Stop any previous automatic Xray selector. RC1 does not migrate or coordinate with `blanc-auto`.

## Keenetic + Entware + XKeen

Requirements:

- Entware mounted at `/opt`;
- `/opt/sbin/xray` with API subcommands;
- `/opt/sbin/xkeen`;
- Xray confdir at `/opt/etc/xray/configs`;
- ARM64, ARMv7, MIPSLE or AMD64 release binary.

### One-command installation

Run as `root` from an interactive SSH session:

```sh
curl -fsSL https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh | sh
```

The bootstrap script:

1. detects `amd64`, `arm64`, `armv7` or `mipsle`;
2. downloads the matching binary and `SHA256SUMS` from the latest GitHub Release;
3. verifies the binary SHA-256 before executing it;
4. downloads the Keenetic installer, init script and configuration template;
5. starts the normal interactive installer through `/dev/tty`;
6. removes all temporary bootstrap files on exit.

No repository clone or release archive is required. The installer still asks for the subscription, score and health URLs, optional speed-test URL, web username and web password.

If `curl` is unavailable but `wget` exists:

```sh
wget -qO- https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh | sh
```

Pin a specific release when reproducibility is required:

```sh
curl -fsSL https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh | KRM_VERSION=v1.0.0-rc.1 sh
```

The bootstrap can also be downloaded and inspected before execution:

```sh
curl -fsSLo /tmp/krm-bootstrap.sh https://raw.githubusercontent.com/jarymor-ux/kee-route-manager/main/install/keenetic/bootstrap.sh
sh /tmp/krm-bootstrap.sh
```

### Installation from an unpacked bundle

```sh
sh install/keenetic/install.sh
```

Both installation paths:

1. check dependencies and architecture;
2. detect the routing fragment;
3. ask for URLs and web credentials;
4. validate the generated configuration;
5. install an Entware init script;
6. start KRM.

Open:

```text
https://ROUTER_IP:9443/
```

## OpenWrt

RC1 supports the procd/firewall4 generation of OpenWrt with an Xray **confdir** at `/etc/xray/configs`.

```sh
sh install/openwrt/install.sh
```

`firewall_mode: existing` is the default. Select `managed` only when the configured redirect/TProxy ports exactly match the existing Xray inbounds.

## Linux + systemd

RC1 expects Xray confdir at `/etc/xray/configs` and Python 3 only for the interactive installer template substitution.

```sh
sudo install/linux-systemd/install.sh
```

The installed service is hardened but runs as root because it must modify the owned Xray fragments and may manage nftables.

## UI on another PC or Raspberry Pi

A browser can always open the controller directly. No local UI installation is required.

To host the same UI on another Linux system and proxy the API:

```sh
sudo install/ui-proxy/install.sh
```

This installs only the UI-proxy role. The controller remains the source of authentication and state.

## Uninstall and rollback

Each platform includes an `uninstall.sh`. It first stops KRM, restores the persistent first-install Xray snapshot, validates it, restarts Xray, and only then removes the executable.

Example:

```sh
sh install/keenetic/uninstall.sh
```

Use `--purge` to remove credentials, TLS files, cache and state after successful restoration.

## Manual commands

```sh
kee-route-manager status --config /path/config.yaml
kee-route-manager benchmark --config /path/config.yaml
kee-route-manager doctor --config /path/config.yaml
kee-route-manager restore-xray --config /path/config.yaml
```
