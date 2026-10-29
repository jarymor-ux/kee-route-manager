# Kee Route Manager

[English](README.md) | [Русский](README.ru.md) | **简体中文**

**版本 2.0.2：**使用 Go 1.27.3 构建，包含 [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809)（`crypto/x509`）修复。仅使用完整且签名验证通过的 `v2.0.2` 发布文件；如尚未发布，应停止安装，不能改用可变分支。旧版 v1.3.3 使用受影响的 Go 1.27.2，文件保持不变。旧 Keenetic 自动生成配置中若有已移除的平台子项，必须先进行[离线迁移](docs/BREAKING_CHANGES.md#201-keeneticnetcraze-only-router-targets)。

**候选版本 2.1.0-rc.3：**此候选版本增加经明确批准的固定版本 XKeen/Xray 安装、默认阻断的全新配置以及安装恢复。Entware、可信工具、现有原生 `xkeen` 策略和 RCI 授权仍需由操作者准备。浏览器界面默认在可信 LAN 使用带明确警告的 HTTP，控制器上游仍使用验证过的 HTTPS。参见[安装流程与待完成的硬件验收](docs/INSTALLER_AUTOMATION_PLAN.md)。先前发布文件保持不变；安装前必须确认此候选版本完整且签名验证通过。

KRM 是**仅面向 Keenetic 和 Netcraze 路由器**的开源 Xray、VPN 订阅、路由健康与节点切换面板。统一的型号、架构与资源要求见[硬件兼容性](docs/HARDWARE.md)。目前仅支持 Xray，不支持 AmneziaWG 或其他 VPN 内核。**`v2.0.2` 面向 stable 渠道；Release 与 Release Candidate 仍独立发布。所有型号在完成硬件验收前仍为 experimental。** 已在一台 Keenetic 上验证 launcher 迁移和通过面板 API 从 GitHub 更新，Xray 进程及设备策略保持不变。重启、断电和故障候选版本的硬件回滚仍待验证。既有版本的标签和发布文件保持不变。

VPN → Nodes 提供 **Автовыбор** 开关，可将单个节点排除在测试、自动选择、故障切换和恢复候选之外。当前活动节点不能禁用。设置在订阅刷新和重启后保留；重新打开开关即可恢复。参见[节点选择接口](docs/API.md#node-automatic-selection)。

面板现在分为路由器和 VPN 两个菜单，提供指标仪表板、有限历史记录和由服务端验证权限的多用户管理。基准测试期间可以执行兼容的路由器操作和手动切换路由；重启操作先取消测试并等待清理。参见[凭据迁移和回滚限制](docs/API.md#credential-migration-and-recovery)。

Router → Settings 提供运行配置表单、校验、版本冲突保护和初始化失败回滚，并支持受 launcher 管理的本地面板域名/HTTPS 端口试用及五分钟确认。地址功能需要单独维护的 v1.2.0+ launcher；普通应用更新不会替换它。路由器 IP、WAN 监听和池大小不在此表单范围内。新交互安装会询问测试间隔、缓存及本地 UI 地址；已有配置不改写。

- `kee-route-managerd`：唯一的状态、Xray 与防火墙控制进程；可独立运行，提供 HTTPS 与 Unix socket API。
- `kee-route-manager-ui`：独立 Web/PWA 与经过 TLS 验证的 API 代理，没有路由器控制逻辑。
- `kee-route-managerctl`：本地控制客户端，配置验证、密码设置和 strict JSON 路由候选检查。
- `kee-route-manager-launcher`：稳定的父进程，管理 daemon 及 Keenetic/Netcraze 本机 UI，验证签名版本并按用户明确请求应用更新。

组件可交叉编译为 Linux arm64、armv7、mipsle 与 MIPS BE `mips`；amd64 仅用于开发工具及管理路由器的独立 UI 主机。健康检查比较 VPN 与 WAN；监控不明确时保持路由。Benchmark 不启用 direct。**Keenetic 的 Xray 故障自动 bypass 尚不支持**。所有平台仍是 experimental。

随附模板通过 `update.enabled: true` 和 `check_interval: 30m` 设置每 30 分钟检查更新；用户通过 UI 按钮或 `update-apply` 命令确认安装。必须保持 `auto_apply: false`。带签名的 protocol-1 bundle 包含 daemon、UI 和 CLI；试运行检查进程身份、API 与状态一致性，提交前失败可返回旧版本。提交后只重启新版本，不恢复旧控制器状态。稳定 launcher 需要单独维护；远程 UI 主机仅管理路由器，需单独运行和维护。

使用兼容的独立 launcher 后，可在“路由器 → 系统”选择 Release Candidate (`rc`) 或 Release (`stable`) 更新渠道。选择会在重启后保留，仅影响更新发现，不会自动安装或启用降级。旧 launcher 需要单独升级；参见[发布与迁移流程](docs/RELEASE.md)。

从 2.0.1 起不再提供 OpenWrt 或独立 Linux/systemd 路由器安装；既有签名发布文件保持不变。Bootstrap 固定版本，在执行任何下载程序之前验证 Ed25519 签名和 SHA256。2.0.1 提供 MIPS BE，KN-2410/2510 仍需实际硬件验收；旧 v1.3.3 不含 BE 文件，不能用 mipsle 替代。

AI 代理应先阅读 [AGENTS.md](AGENTS.md)，再按照 [完整安装流程](docs/AGENT_INSTALL.md) 执行 SSH、备份、配置、core/UI 安装、TLS、验证、卸载及回滚。

安装时仅使用完整且验证通过的签名版本 `v2.0.2`，不要使用可变的 `main` 或 `/releases/latest`。在发布文件尚未齐全时停止安装，不要替换为旧版本。

参见 [限制](docs/KNOWN_LIMITATIONS.md) 与 [发布流程](docs/RELEASE.md)。自动化验证见 [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions)。许可证：[Apache-2.0](LICENSE)。

发布流程使用两个分支：`main` 为稳定发布线，`release-candidate` 为候选版本。版本号来自不可变 Git 标签 `vX.Y.Z` 或 `vX.Y.Z-rc.N`，无需版本文件。推送分支运行检查，推送标签通过分支和渠道检查后发布签名版本。未打标签的本地构建标记为 `dev`。发布稳定渠道不代表已完成设备型号和未执行硬件场景的验收；参见[发布流程](docs/RELEASE.md)。
