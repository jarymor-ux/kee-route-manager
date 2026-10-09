# Kee Route Manager

[English](README.md) | [Русский](README.ru.md) | **简体中文**

KRM 管理 Xray 节点池、订阅与故障切换。**Release `v1.3.2` 与 Release Candidate `v1.3.2-rc.1` 使用独立发布渠道。平台支持在完成硬件验收前仍为 experimental。** 已在一台 Keenetic 上验证 launcher 迁移和通过面板 API 从 GitHub 更新，Xray 进程及设备策略保持不变。重启、断电和故障候选版本的硬件回滚仍待验证。既有版本的标签和发布文件保持不变。

面板现在分为路由器和 VPN 两个菜单，提供指标仪表板、有限历史记录和由服务端验证权限的多用户管理。基准测试期间可以执行兼容的路由器操作和手动切换路由；重启操作先取消测试并等待清理。参见[凭据迁移和回滚限制](docs/API.md#credential-migration-and-recovery)。

Router → Settings 提供运行配置表单、校验、版本冲突保护和初始化失败回滚，并支持受 launcher 管理的本地面板域名/HTTPS 端口试用及五分钟确认。地址功能需要单独维护的 v1.2.0+ launcher；普通应用更新不会替换它。路由器 IP、WAN 监听和池大小不在此表单范围内。新交互安装会询问测试间隔、缓存及本地 UI 地址；已有配置不改写。

- `kee-route-managerd`：唯一的状态、Xray 与防火墙控制进程；可独立运行，提供 HTTPS 与 Unix socket API。
- `kee-route-manager-ui`：独立 Web/PWA 与经过 TLS 验证的 API 代理，没有路由器控制逻辑。
- `kee-route-managerctl`：本地控制客户端，配置验证、密码设置和 strict JSON 路由候选检查。
- `kee-route-manager-launcher`：稳定的父进程，管理 daemon 及 Keenetic/OpenWrt 本机 UI，验证签名版本并按用户明确请求应用更新。

Linux amd64、arm64、armv7、mipsle 均提供独立组件。健康检查比较 VPN 与 WAN；监控不明确时保持路由。Benchmark 不启用 direct。Linux/OpenWrt managed nftables 支持独立 bypass；**Keenetic 的 Xray 故障自动 bypass 尚不支持**。所有平台仍是 experimental。

随附模板通过 `update.enabled: true` 和 `check_interval: 30m` 设置每 30 分钟检查更新；用户通过 UI 按钮或 `update-apply` 命令确认安装。必须保持 `auto_apply: false`。带签名的 protocol-1 bundle 包含 daemon、UI 和 CLI；试运行检查进程身份、API 与状态一致性，提交前失败可返回旧版本。提交后只重启新版本，不恢复旧控制器状态。稳定 launcher 需要单独维护；Linux UI 保留独立的 DynamicUser 服务，也需要单独更新。

使用兼容的独立 launcher 后，可在“路由器 → 系统”选择 Release Candidate (`rc`) 或 Release (`stable`) 更新渠道。选择会在重启后保留，仅影响更新发现，不会自动安装或启用降级。旧 launcher 需要单独升级；参见[发布与迁移流程](docs/RELEASE.md)。

Linux/OpenWrt 的 `firewall_mode: managed` 仍支持全新安装和普通运行，但更新功能目前仅支持发现新版本；在实现托管防火墙的只读一致性检查之前，禁止通过更新器暂存、应用和试运行候选版本。Keenetic 及 Linux/OpenWrt 的 `existing` 模式可通过 launcher 应用更新，但这不代表 Keenetic 支持自动 bypass。详见[限制](docs/KNOWN_LIMITATIONS.md)。Bootstrap 固定版本，在执行任何下载程序之前验证 Ed25519 签名和 SHA256。

AI 代理应先阅读 [AGENTS.md](AGENTS.md)，再按照 [完整安装流程](docs/AGENT_INSTALL.md) 执行 SSH、备份、配置、core/UI 安装、TLS、验证、卸载及回滚。

安装时使用已发布的签名版本 `v1.3.2`，不要使用可变的 `main` 或 `/releases/latest`。在发布文件尚未齐全时停止安装，不要替换为旧版本。

参见 [限制](docs/KNOWN_LIMITATIONS.md) 与 [发布流程](docs/RELEASE.md)。自动化验证见 [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions)。许可证：[Apache-2.0](LICENSE)。

发布流程使用两个分支：`main` 为稳定发布线，`release-candidate` 为候选版本。版本号来自不可变 Git 标签 `vX.Y.Z` 或 `vX.Y.Z-rc.N`，无需版本文件。推送分支运行检查，推送标签通过分支和渠道检查后发布签名版本。未打标签的本地构建标记为 `dev`。发布稳定渠道不代表已完成设备型号和未执行硬件场景的验收；参见[发布流程](docs/RELEASE.md)。
