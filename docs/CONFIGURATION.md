# Configuration

KRM uses a strict, declarative YAML subset. Unknown fields, duplicate keys, tabs, unsupported anchors and malformed indentation are rejected. Configuration files are never executed as shell code.

Run validation before restart:

```sh
kee-route-manager validate --config /etc/kee-route-manager/config.yaml
```

## Instance

```yaml
schema_version: 1
instance:
  name: Kee Route Manager
  role: controller   # controller or ui-proxy
```

`controller` runs subscriptions, Xray control, API and UI. `ui-proxy` only serves the UI and proxies API calls to another controller.

## Web and TLS

```yaml
web:
  enabled: true
  listen: "0.0.0.0:9443"
  credentials_file: /etc/kee-route-manager/credentials.json
  session_ttl: 24h
  tls:
    enabled: true
    auto_generate: true
    cert_file: /etc/kee-route-manager/tls.crt
    key_file: /etc/kee-route-manager/tls.key
    hosts: []
```

The self-signed certificate is generated only when both configured TLS files are absent. Replace them with a trusted certificate if required.

## Subscriptions

```yaml
subscriptions:
  max_sources: 20
  max_nodes: 500
  cache_ttl: 168h
  refresh_interval: 30m
  request_timeout: 20s
  max_response_bytes: 4MiB
  sources:
    - id: primary
      name: Provider A
      url: "https://provider.example/subscription"
      enabled: true
      headers: {}
```

Supported payloads in RC1:

- plain text containing VLESS URIs;
- standard or URL-safe base64 text containing VLESS URIs;
- VLESS Reality over TCP;
- VLESS WebSocket over TLS.

The parser ignores unsupported lines and rejects a source if no supported node remains.

## Targets

No external target is built in. At least one `score` and one `health` target are required.

```yaml
targets:
  - id: latency_a
    name: Latency endpoint
    url: "https://your-endpoint.example/health"
    role: score
    weight: 2
    policy: 2xx3xx
    max_response_bytes: 64KiB
```

Policies are `2xx3xx` or `exact:204`-style exact status checks. Health succeeds only when a strict majority of configured health targets succeeds.

## Pool and switching

```yaml
pool:
  size: 5
  provider_diversity:
    enabled: false
    max_per_provider: 1
```

Diversity is optional and soft. It never makes a working single-provider setup invalid.

```yaml
health:
  interval: 15s
  failure_threshold: 2
  recovery_threshold: 2
  hot_pool_freshness: 5m
  provider_retry_backoff: [15s, 30s, 1m, 2m, 5m, 10m]
```

```yaml
benchmark:
  full_interval: 6h
  batch_size: 20
  latency_workers: 8
  finalists: 6
  min_improvement_percent: 15
  switch_cooldown: 10m
  stability_before_upgrade: 10m
```

The active working node is not replaced merely because another result is slightly faster. Scheduled upgrades require the configured improvement, stability and cooldown.

## Adaptive speed measurement

```yaml
benchmark:
  speed:
    enabled: true
    url_template: "https://your-speed-endpoint.example/download?bytes={bytes}"
    warmup_bytes: 8MiB
    min_sample_bytes: 64MiB
    max_sample_bytes: 512MiB
    target_duration: 6s
    repetitions: 3
```

`{bytes}` is replaced with the requested byte count. KRM adapts the next sample toward `target_duration`, takes the median and never writes the response body to disk.

## Xray

`config_dir` must be an Xray confdir layout. `base_routing_file` must be strict JSON and contain the routing rule currently sending the configured inbound tags to one of `replace_outbound_tags`.

```yaml
xray:
  api_address: "127.0.0.1:10085"
  balancer_tag: krm-main
  route:
    inbound_tags: [redirect, tproxy]
    replace_outbound_tags: [vless-reality]
```

KRM refuses first bootstrap when no matching rule is found.

## Firewall

For OpenWrt or Linux:

```yaml
firewall_mode: existing
```

This leaves transparent routing entirely to the existing system.

Managed IPv4 mode additionally requires LAN interfaces, TCP redirect port, UDP TProxy port, mark and route table. The ports must match existing Xray transparent inbounds.

## Updates

```yaml
update:
  enabled: true
  channel: rc
  manifest_url: "https://.../manifest-rc.json"
  signature_url: "https://.../manifest-rc.json.sig"
  public_key: "base64-ed25519-public-key"
  auto_apply: false
```

The public key is not a secret. The corresponding private release key must never be committed or copied to the managed device.
