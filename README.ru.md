# Kee Route Manager

[English](README.md) | **Русский** | [简体中文](README.zh-CN.md)

KRM управляет проверенным горячим пулом Xray, подписками и переключением маршрутов. **1.1.0-rc.4 — экспериментальный prerelease.** Предыдущая приватная сборка работала на одном Keenetic; аппаратная проверка нового updater ещё не завершена. Опубликованные теги и assets прежних версий неизменны.

- `kee-route-managerd` — единственный владелец state, Xray и firewall; работает без UI, HTTPS API и локальный Unix socket.
- `kee-route-manager-ui` — отдельный процесс Web/PWA и proxy; без router-specific логики, shell и пароля администратора.
- `kee-route-managerctl` — клиент daemon; локальная проверка конфигурации, настройка credentials и просмотр strict-JSON routing candidates.
- `kee-route-manager-launcher` — стабильный родитель daemon и локального UI на Keenetic/OpenWrt; проверяет подписанные версии и применяет обновления по явному запросу.

Все компоненты собираются для Linux amd64/arm64/armv7/mipsle. Отказ health endpoint сравнивается с WAN; при недостаточных данных маршрут сохраняется. Benchmark не включает direct. Независимый от Xray bypass реализован для managed nftables Linux/OpenWrt; **Keenetic automatic bypass при падении Xray не поддерживается**. Все платформы пока experimental.

В поставляемых шаблонах `update.enabled: true` и `check_interval: 30m` задают проверку обновлений каждые 30 минут; применение запускает пользователь кнопкой UI или командой `update-apply`. `auto_apply: false` обязателен. Подписанный protocol-1 bundle содержит daemon, UI и CLI. После проверки процессов, API и reconciliation неудачный trial возвращает предыдущую версию; после commit перезапускается новая версия без возврата старого состояния. Стабильный launcher обновляется вручную; Linux UI сохраняет отдельный DynamicUser-сервис и тоже обслуживается отдельно.

На Linux/OpenWrt с `firewall_mode: managed` установка и обычная работа KRM поддерживаются, но для обновлений доступна только проверка новых версий: скачивание через updater, применение и candidate trial запрещены до реализации read-only reconciliation управляемого firewall. Keenetic и режим `existing` на Linux/OpenWrt допускают применение через launcher; это не добавляет Keenetic автоматический bypass. Подробности — в [ограничениях](docs/KNOWN_LIMITATIONS.md). Production bootstrap использует одну фиксированную версию и проверяет Ed25519 и SHA256 до исполнения скачанных программ.

Чтобы передать установку своему AI-агенту, достаточно ссылки на репозиторий: [глобальные инструкции AGENTS.md](AGENTS.md) и [полный порядок установки](docs/AGENT_INSTALL.md). Там описаны SSH, backup, подготовка config без секретов в Git, выбор routing tags, core-only, локальный/удалённый UI, TLS, readiness, удаление и rollback.

После публикации подписанного release и подготовки приватной конфигурации:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh https://github.com/jarymor-ux/kee-route-manager/releases/download/v1.1.0-rc.4/bootstrap-keenetic.sh
KRM_MODE=core KRM_CONFIG_FILE=/root/krm-install/config.yaml sh /tmp/krm-bootstrap.sh
```

Для OpenWrt/Linux используйте `bootstrap-openwrt.sh`/`bootstrap-linux.sh`. Ограничения: [KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md). Автоматические проверки: [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions). Изменения: [CHANGELOG.md](CHANGELOG.md).

Лицензия [Apache-2.0](LICENSE) выбрана владельцем.
