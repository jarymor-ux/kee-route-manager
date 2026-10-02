# Kee Route Manager

[English](README.md) | [Русский](README.ru.md) | **简体中文**

**Kee Route Manager (KRM)** 是一个面向路由器和 Linux 网关的本地 Xray 路由控制器。它将多个订阅源合并到一个去重节点池中，在 Xray 中保持一个可配置的热备池，在无需等待完整基准测试的情况下将新连接切换到已验证的备用节点，并提供自适应 Web/PWA 界面。

当前版本：**1.0.0-rc.1**。

## RC1 支持范围

| 类别 | 支持内容 |
|---|---|
| 平台 | Keenetic + Entware + XKeen；OpenWrt + procd；Linux + systemd |
| CPU 架构 | amd64、arm64、armv7、mipsle |
| VPN 核心 | Xray |
| 节点 | VLESS Reality/TCP；VLESS WebSocket/TLS |
| 订阅 | 纯 URI 列表；Base64 编码的 URI 列表；最多 20 个订阅源 |
| 连接池 | 统一去重节点池；默认 5 个热备节点；数量可配置 |
| 故障切换 | 优先使用已验证的备用节点；仅当所有 VPN 备用节点均失效时才使用直连路由 |
| 界面 | 内置自适应 Web/PWA；可选的 Linux UI 代理，可部署在 PC/Raspberry Pi 上 |
| 身份认证 | 用户自定义登录名和密码；PBKDF2-SHA256；会话与 CSRF 防护 |
| 更新 | Ed25519 签名清单；SHA-256 资源校验；原子替换与回滚 |

## 故障切换机制

```text
订阅源
        │
        ▼
去重节点池
        │
        ▼
基准测试 + 健康检查历史
        │
        ▼
热备池（默认：5 个 Xray 出站）
        │
        ├─ 当前 VPN 节点
        ├─ 已验证的备用节点
        ├─ 已验证的备用节点
        ├─ 已验证的备用节点
        └─ 已验证的备用节点
```

1. 默认每 15 秒通过 Xray 检查一次当前路径。
2. 连续两轮健康检查失败后，KRM 会探测已加载的备用槽位。
3. 新连接通过 Xray 本地 API 切换到第一个可用的备用节点。
4. 完整基准测试在连接恢复后运行，而不是在故障切换前运行。
5. 如果所有 VPN 槽位均不可用，KRM 会明确地将受管理流量切换到 `direct`。
6. 在直连模式下，KRM 会继续探测备用槽位；连续两次检查成功后恢复 VPN。
7. 远端服务器失效后，已建立的 TCP/UDP 会话无法迁移；应用程序会通过新路径重新连接。

基准测试数据会直接流入 `io.Discard`，下载的测速数据不会写入磁盘。

## 仓库结构

```text
cmd/kee-route-manager/       后台服务与 CLI
cmd/krm-release-tool/        Ed25519 发布工具
internal/auth/               凭据、会话与 CSRF 支持
internal/bench/              延迟、健康检查与自适应测速
internal/config/             严格的 YAML 子集与校验
internal/core/               调度器、连接池、故障切换与操作
internal/platform/           Keenetic、OpenWrt 与 Linux 适配器
internal/subscription/       获取、缓存、解析与去重
internal/update/             签名自更新与回滚
internal/web/                HTTPS API 与内置 PWA
internal/xray/               受管理配置片段、API 切换与回滚
configs/                     平台配置模板
install/                     平台安装与卸载脚本
web/                         前端源码
```

## 安装

请阅读 [docs/INSTALL.md](docs/INSTALL.md)。安装程序采用交互式方式，因为 KRM 不内置订阅地址，也不内置健康检查、评分或测速目标。

从解压后的发布包在 Keenetic 上安装：

```sh
sh install/keenetic/install.sh
```

安装程序不会静默迁移 `blanc-auto`。RC1 仅支持全新安装。

## 本地开发

KRM 不包含第三方 Go 依赖。

```sh
go test ./...
go vet ./...
go build ./cmd/kee-route-manager
go build ./cmd/krm-release-tool
```

验证配置：

```sh
./kee-route-manager validate --config configs/linux-systemd.yaml
```

在不将密码放入进程参数的情况下创建凭据：

```sh
printf '%s\n' 'a-long-password' |
  ./kee-route-manager passwd \
    --config /etc/kee-route-manager/config.yaml \
    --username admin \
    --password-stdin
```

## 文档

- [架构](docs/ARCHITECTURE.md)
- [配置](docs/CONFIGURATION.md)
- [安装](docs/INSTALL.md)
- [安全](docs/SECURITY.md)
- [破坏性变更](docs/BREAKING_CHANGES.md)
- [更新与发布格式](docs/UPDATE_FORMAT.md)
- [已知限制](docs/KNOWN_LIMITATIONS.md)
- [API 概览](docs/API.md)
- [测试报告](docs/TEST_REPORT.md)
- [发布流程](docs/RELEASE.md)

## 发布状态

当前版本为候选发布版。发布脚本包含单元测试、静态检查、配置校验、安装脚本语法检查和交叉编译。向目标路由器安装时仍应采用受控发布流程，并确保保留设备的恢复通道。
