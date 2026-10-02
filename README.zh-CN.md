# Kee Route Manager

[English](README.md) | [Русский](README.ru.md) | **简体中文**

KRM 管理 Xray 节点池、订阅与故障切换。**1.0.0-rc.2 是实验性预发布版本；真实 Keenetic 硬件验收将在下一阶段执行，目前尚未完成。** RC1 标签与文件保持不变。

- `kee-route-managerd`：唯一的状态、Xray 与防火墙控制进程；可独立运行，提供 HTTPS 与 Unix socket API。
- `kee-route-manager-ui`：独立 Web/PWA 与经过 TLS 验证的 API 代理，没有路由器控制逻辑。
- `kee-route-managerctl`：本地控制客户端，配置验证、密码设置和 strict JSON 路由候选检查。

Linux amd64、arm64、armv7、mipsle 均提供独立组件。健康检查比较 VPN 与 WAN；监控不明确时保持路由。Benchmark 不启用 direct。Linux/OpenWrt managed nftables 支持独立 bypass；**Keenetic 的 Xray 故障自动 bypass 尚不支持**。所有平台仍是 experimental。

自动更新应用已禁用，直到完成 A/B launcher。Bootstrap 固定版本，在执行任何下载程序之前验证 Ed25519 签名和 SHA256。

AI 代理应先阅读 [AGENTS.md](AGENTS.md)，再按照 [完整安装流程](docs/AGENT_INSTALL.md) 执行 SSH、备份、配置、core/UI 安装、TLS、验证、卸载及回滚。

参见 [限制](docs/KNOWN_LIMITATIONS.md) 与 [发布流程](docs/RELEASE.md)。自动化验证见 [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions)。许可证：[Apache-2.0](LICENSE)。
