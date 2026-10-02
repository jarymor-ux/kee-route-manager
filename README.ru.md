# Kee Route Manager

[English](README.md) | **Русский** | [简体中文](README.zh-CN.md)

KRM управляет проверенным горячим пулом Xray, подписками и переключением маршрутов. **1.0.0-rc.2 — experimental prerelease. Проверка на реальном Keenetic назначена отдельным этапом и пока не выполнена.** RC1 не изменён.

- `kee-route-managerd` — единственный владелец state, Xray и firewall; работает без UI, HTTPS API и локальный Unix socket.
- `kee-route-manager-ui` — отдельный процесс Web/PWA и proxy; без router-specific логики, shell и пароля администратора.
- `kee-route-managerctl` — клиент daemon; локальная проверка конфигурации, настройка credentials и просмотр strict-JSON routing candidates.

Все компоненты собираются для Linux amd64/arm64/armv7/mipsle. Отказ health endpoint сравнивается с WAN; при недостаточных данных маршрут сохраняется. Benchmark не включает direct. Независимый от Xray bypass реализован для managed nftables Linux/OpenWrt; **Keenetic automatic bypass при падении Xray не поддерживается**. Все платформы пока experimental.

Небезопасная самозамена binary удалена: `update.apply` отключён до реализации A/B launcher. Проверка обновлений различает RC и stable. Production bootstrap использует одну фиксированную версию, Ed25519 и SHA256 до исполнения скачанных программ.

Чтобы передать установку своему AI-агенту, достаточно ссылки на репозиторий: [глобальные инструкции AGENTS.md](AGENTS.md) и [полный порядок установки](docs/AGENT_INSTALL.md). Там описаны SSH, backup, подготовка config без секретов в Git, выбор routing tags, core-only, локальный/удалённый UI, TLS, readiness, удаление и rollback.

После подготовки приватной конфигурации:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.0.0-rc.2/bootstrap-keenetic.sh
KRM_MODE=core KRM_CONFIG_FILE=/root/krm-install/config.yaml sh /tmp/krm-bootstrap.sh
```

Для OpenWrt/Linux используйте `bootstrap-openwrt.sh`/`bootstrap-linux.sh`. Ограничения: [KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md). Автоматические проверки: [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions). Изменения: [CHANGELOG.md](CHANGELOG.md).

Лицензия [Apache-2.0](LICENSE) выбрана владельцем.
