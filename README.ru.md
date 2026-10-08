# Kee Route Manager

[English](README.md) | **Русский** | [简体中文](README.zh-CN.md)

KRM управляет проверенным горячим пулом Xray, подписками и переключением маршрутов. **Доступны Release `v1.2.0` и Release Candidate `v1.2.0-rc.1` в отдельных каналах. Поддержка платформ остаётся experimental до аппаратной приёмки.** На одном Keenetic проверены установка launcher и обновление из GitHub через API панели с сохранением работающего Xray и выбранной политики устройств. Перезагрузка, сбой питания и аппаратный rollback при неисправном кандидате ещё не проверены. Опубликованные теги и assets прежних версий неизменны.

В панели два меню: «Роутер» и «VPN», дашборды с ограниченной историей и несколько пользователей с серверной проверкой прав. Во время бенчмарка доступны совместимые действия роутера и ручное переключение маршрута; перезапуски сначала отменяют тест и дожидаются очистки. См. [миграцию учётных записей и ограничения отката](docs/API.md#credential-migration-and-recovery).

В «Роутер → Настройки» доступны параметры тестирования, проверок VPN, переключения и загрузки подписок. Сохранение проверяет конфиг и его ревизию, перечитывает настройки без смены PID daemon и возвращает прежний YAML при ошибке запуска. Здесь же меняются DNS-имя и HTTPS-порт локальной панели: новый адрес нужно подтвердить за пять минут. Для этой функции отдельно обновите launcher до v1.2.0+; обычный update приложения его не заменяет. IP роутера, WAN и размер пула через форму не меняются. Новый RU/EN мастер спрашивает интервал, кеширование и локальный адрес UI; подготовленный конфиг сохраняется.

- `kee-route-managerd` — единственный владелец state, Xray и firewall; работает без UI, HTTPS API и локальный Unix socket.
- `kee-route-manager-ui` — отдельный процесс Web/PWA и proxy; без router-specific логики, shell и пароля администратора.
- `kee-route-managerctl` — клиент daemon; локальная проверка конфигурации, настройка credentials и просмотр strict-JSON routing candidates.
- `kee-route-manager-launcher` — стабильный родитель daemon и локального UI на Keenetic/OpenWrt; проверяет подписанные версии и применяет обновления по явному запросу.

Все компоненты собираются для Linux amd64/arm64/armv7/mipsle. Отказ health endpoint сравнивается с WAN; при недостаточных данных маршрут сохраняется. Benchmark не включает direct. Независимый от Xray bypass реализован для managed nftables Linux/OpenWrt; **Keenetic automatic bypass при падении Xray не поддерживается**. Все платформы пока experimental.

В поставляемых шаблонах `update.enabled: true` и `check_interval: 30m` задают проверку обновлений каждые 30 минут; применение запускает пользователь кнопкой UI или командой `update-apply`. `auto_apply: false` обязателен. Подписанный protocol-1 bundle содержит daemon, UI и CLI. После проверки процессов, API и reconciliation неудачный trial возвращает предыдущую версию; после commit перезапускается новая версия без возврата старого состояния. Стабильный launcher обновляется вручную; Linux UI сохраняет отдельный DynamicUser-сервис и тоже обслуживается отдельно.

В «Роутер → Система» можно выбрать Release Candidate (`rc`) или Release (`stable`) при установленном совместимом launcher. Выбор сохраняется после перезапуска, меняет только поиск обновлений и не включает автоматическую установку или downgrade. Для старого launcher требуется отдельное обновление; см. [порядок релизов и миграции](docs/RELEASE.md).

На Linux/OpenWrt с `firewall_mode: managed` установка и обычная работа KRM поддерживаются, но для обновлений доступна только проверка новых версий: скачивание через updater, применение и candidate trial запрещены до реализации read-only reconciliation управляемого firewall. Keenetic и режим `existing` на Linux/OpenWrt допускают применение через launcher; это не добавляет Keenetic автоматический bypass. Подробности — в [ограничениях](docs/KNOWN_LIMITATIONS.md). Production bootstrap использует одну фиксированную версию и проверяет Ed25519 и SHA256 до исполнения скачанных программ.

Чтобы передать установку своему AI-агенту, достаточно ссылки на репозиторий: [глобальные инструкции AGENTS.md](AGENTS.md) и [полный порядок установки](docs/AGENT_INSTALL.md). Там описаны SSH, backup, подготовленная или интерактивная конфигурация, выбор routing tags, core-only, локальный/удалённый UI, TLS, readiness, удаление и rollback.

Для новой установки скачайте bootstrap своей платформы из **одного неизменяемого подписанного релиза с интерактивным установщиком** и запустите без config:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh RELEASE_URL
sh /tmp/krm-bootstrap.sh
```

Замените `RELEASE_URL` на URL asset `bootstrap-keenetic.sh`, `bootstrap-openwrt.sh` или `bootstrap-linux.sh` выбранного релиза. Используйте подписанный релиз с интерактивным установщиком; ранее опубликованные assets не изменяются.

После проверки всех release assets wizard на **русском или английском** настраивает пути Xray, явно выбранные routing tags, подписки и headers, score/health targets, размер пула, необязательную проверку скорости и поиск обновлений. Без `KRM_MODE` используется `local-ui`; для установки только controller запустите `KRM_MODE=core sh /tmp/krm-bootstrap.sh`. Нужен интерактивный терминал. Wizard создаёт приватный controller config и проверяет его; в `local-ui` также автоматически создаётся UI config с upstream `https://127.0.0.1:9443` и публичным сертификатом controller в `controller-ca.crt`. TLS verification остаётся включённой, второй YAML готовить вручную не требуется.

Routing tags нельзя угадывать: выберите реальные значения из Xray rules через `kee-route-managerctl route-candidates --file PATH`. Health targets желательно размещать на независимых hostname у разных операторов, чтобы сбой одного сервера не выглядел отказом VPN. Безопасные defaults сохраняют существующее управление firewall, loopback API, выключенный speed test и `auto_apply: false`. Поиск обновлений не включает автоматическое применение. Секретные URL, headers и credentials не должны попадать в логи.

Существующий advanced/noninteractive вариант пропускает wizard:

```sh
KRM_MODE=core \
KRM_CONFIG_FILE=/root/krm-install/config.yaml \
sh /tmp/krm-bootstrap.sh
```

При prepared config для `local-ui` по-прежнему нужен `KRM_UI_CONFIG_FILE`: подготовьте оба YAML с согласованными TLS-настройками. Для отдельного `KRM_MODE=ui` нужны подготовленный UI config, trusted upstream CA через `KRM_UPSTREAM_CA_FILE` и authenticated SSH tunnel; wizard не обходит эти требования. Existing installation не перезаписывается: restore/uninstall/reinstall описаны в [runbook](docs/AGENT_INSTALL.md). Ограничения: [KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md). Автоматические проверки: [GitHub Actions](https://github.com/jarymor-ux/kee-route-manager/actions). Изменения: [CHANGELOG.md](CHANGELOG.md).

Лицензия [Apache-2.0](LICENSE) выбрана владельцем.

Две ветки: `main` — стабильная линия, `release-candidate` — кандидаты. Номер версии берётся из неизменяемого Git-тега `vX.Y.Z` или `vX.Y.Z-rc.N`; отдельного файла версии нет. Push ветки запускает проверки, публикацию запускает тег с проверкой ветки и канала. Локальные сборки без тега отмечаются `dev`. Публикация stable-канала не подтверждает проверку моделей роутеров и невыполненных аппаратных сценариев; [порядок публикации](docs/RELEASE.md).
