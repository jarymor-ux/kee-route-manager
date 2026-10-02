# Kee Route Manager

[English](README.md) | [Русский](README.ru.md) | **简体中文**

KRM 管理 Xray 节点池、订阅与故障切换。**1.1.0-rc.3 是实验性预发布版本。** 先前的私有构建已在一台 Keenetic 上运行，但新更新器的硬件验证尚未完成。既有版本的标签和发布文件保持不变。

- `kee-route-managerd`：唯一的状态、Xray 与防火墙控制进程；可独立运行，提供 HTTPS 与 Unix socket API。
- `kee-route-manager-ui`：独立 Web/PWA 与经过 TLS 验证的 API 代理，没有路由器控制逻辑。
- `kee-route-managerctl`：本地控制客户端，配置验证、密码设置和 strict JSON 路由候选检查。
- `kee-route-manager-launcher`：稳定的父进程，管理 daemon 及 Keenetic/OpenWrt 本机 UI，验证签名版本并按用户明确请求应用更新。

Linux amd64、arm64、armv7、mipsle 均提供独立组件。健康检查比较 VPN 与 WAN；监控不明确时保持路由。Benchmark 不启用 direct。Linux/OpenWrt managed nftables 支持独立 bypass；**Keenetic 的 Xray 故障自动 bypass 尚不支持**。所有平台仍是 experimental。

随附模板通过 `update.enabled: true` 和 `check_interval: 30m` 设置每 30 分钟检查更新；用户通过 UI 按钮或 `update-apply` 命令确认安装。必须保持 `auto_apply: false`。带签名的 protocol-1 bundle 包含 daemon、UI 和 CLI；试运行检查进程身份、API 与状态一致性，提交前失败可返回旧版本。提交后只重启新版本，不恢复旧控制器状态。稳定 launcher 需要单独维护；Linux UI 保留独立的 DynamicUser 服务，也需要单独更新。

Linux/OpenWrt 的 `firewall_mode: managed` 仍支持全新安装和普通运行，但更新功能目前仅支持发现新版本；在实现托管防火墙的只读一致性检查之前，禁止通过更新器暂存、应用和试运行候选版本。Keenetic 及 Linux/OpenWrt 的 `existing` 模式可通过 launcher 应用更新，但这不代表 Keenetic 支持自动 bypass。详见[限制](docs/KNOWN_LIMITATIONS.md)。Bootstrap 固定版本，在执行任何下载程序之前验证 Ed25519 签名和 SHA256。

AI 代理应先阅读 [AGENTS.md](AGENTS.md)，再按照 [完整安装流程](docs/AGENT_INSTALL.md) 执行 SSH、备份、配置、core/UI 安装、TLS、验证、卸载及回滚。

安装时使用已发布的签名版本 `v1.1.0-rc.3`，不要使用可变的 `main` 或 `/releases/latest`。在发布文件尚未齐全时停止安装，不要替换为旧版本。

参见 [限制](docs/KNOWN_LIMITATIONS.md) 与 [发布流程](docs/RELEASE.md)。自动化验证见 [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions)。许可证：[Apache-2.0](LICENSE)。
