# Полная установка Kee Route Manager для AI-агента

Этот файл — инструкция от ссылки на репозиторий до проверенной установки. Прочитайте также корневой `AGENTS.md`. Текущие каналы: **v1.2.0** (Release) и **v1.2.0-rc.1** (Release Candidate). Поддержка платформ остаётся experimental до аппаратной приёмки каждой платформы; контейнерные проверки её не заменяют. Для установки используйте полный подписанный набор одного неизменяемого релиза; старые assets не обновляются. KRM требует существующий рабочий Xray; автоматическую установку Entware/XKeen/Xray этот проект не выполняет.

**Прежний v1.0.0-rc.2 не использовать для новой установки:** его раздельные секции Xray `routing` перезаписывают друг друга. В новой версии routing объединён в выбранном base-файле и проверен с реальным Xray. Прежние опубликованные assets остаются неизменными.

## 1. Получить входные данные и подключиться

Нужны адрес роутера/PC, SSH-порт, пользователь с административными правами, SSH-ключ/доступ через агент, подтверждённый fingerprint, свободное место, выбранный режим, приватные подписки и минимум два независимых health URL. Секреты запрашивать безопасным способом, не писать в Git/отчёт/аргументы процессов. Не угадывать SSH-порт: административный SSH Keenetic и Linux-shell Entware — разные среды; Entware часто использует 222.

```sh
ssh -p SSH_PORT USER@ROUTER_ADDRESS
id
uname -a
uname -m
df -h
command -v curl
command -v openssl
openssl version
```

Сверьте fingerprint по независимому каналу. Не используйте `StrictHostKeyChecking=no`. Для Keenetic проверьте `/opt` и Linux-shell, а не только встроенный CLI:

```sh
test -d /opt
test -x /opt/sbin/xray
test -x /opt/sbin/xkeen
/opt/sbin/xray version
/opt/sbin/xray run -test -confdir /opt/etc/xray/configs
```

На OpenWrt/Linux замените binary/confdir на `/usr/bin/xray` и `/etc/xray/configs`. Проверьте API-команды Xray. Managed firewall требует `nft`, `ip`, точного совпадения redirect/TProxy портов с существующими inbounds. Не ставить новую версию поверх неизвестного scheduler.

Проверьте строгий JSON во всех файлах `*.json`, единственную пользовательскую секцию `routing` в выбранном base-файле, фактический asset directory и семантику команд XKeen. Перезапуск должен завершаться синхронно, а status возвращать ошибку при отсутствии основного Xray; временные процессы benchmark не подтверждают его работу. Подробности настройки — в [CONFIGURATION.md](CONFIGURATION.md).

Официальная инструкция [Entware для Keenetic](https://support.keenetic.com/eu/titan/kn-1812/en/20980-installing-the-entware-repository-on-a-usb-drive.html) объясняет подготовку USB/OPKG и отдельный порт SSH. OpenSSL должен поддерживать [Ed25519 через pkeyutl](https://docs.openssl.org/4.0/man1/openssl-pkeyutl/). Если доверенного OpenSSL/curl/CA нет, сначала установить их пакетным менеджером платформы; bootstrap завершается до исполнения скачанных программ.

## 2. Инвентаризация и приватная резервная копия

Проверьте процессы, init/procd/systemd, cron, Xray routing, firewall/policy rules. Сохраните резервную копию вне репозитория, каталог mode 0700, архив mode 0600. На роутере оставьте независимую SSH-сессию и рабочий путь восстановления.

```sh
ps w
ls /opt/etc/init.d /etc/init.d 2>/dev/null
crontab -l
```

При переходе с прежнего проекта найдите его фактические пути/процессы на устройстве, остановите scheduler/watchdog, восстановите routing и WAN, проверьте Xray. Удаляйте только идентифицированные файлы. Инвентаризация и удаление старой установки выполняются на аппаратном этапе с владельцем, а не на компьютере разработки.

## 3. Получить одну фиксированную версию

На компьютере агента:

```sh
git clone https://github.com/jarymor-ux/kee-route-manager.git
cd kee-route-manager
git checkout RELEASE_TAG
```

Читайте release notes и `internal/releasetrust/public.key`. Для этой версии используется закреплённый release signing key; ключ доверять через аутентифицированный репозиторий/канал владельца, а не через файл, скачанный вместе с потенциально подменённой подписью. Bootstrap содержит pinned public key. Первое получение bootstrap защищено HTTPS/GitHub trust; для усиления проверьте подпись и его digest на доверенном компьютере командой `scripts/verify-release.py` после скачивания assets.

Замените `RELEASE_TAG` на неизменяемый tag выбранного релиза с интерактивным установщиком. `RELEASE_URL` ниже — полный HTTPS URL bootstrap asset той же версии: `bootstrap-keenetic.sh`, `bootstrap-openwrt.sh` или `bootstrap-linux.sh`. Сначала убедитесь, что опубликован весь подписанный набор. Если release ещё отсутствует, установка по этим ссылкам должна остановиться, а не подменять версию. Не используйте `main` или `/releases/latest` для установки. Версия, бинарники, сервисы и конфигурации должны совпадать.

## 4. Выбрать подготовленную или интерактивную конфигурацию

При новой установке без `KRM_CONFIG_FILE` приватный config создаёт RU/EN wizard после полной проверки signed assets. Подготовка YAML вручную необязательна; предварительно определите реальные Xray paths и routing tags, subscriptions и независимые health targets. Для prepared/noninteractive пути выполните подготовку ниже: если передан `KRM_CONFIG_FILE`, wizard не запускается.

Скопируйте подходящий шаблон `configs/keenetic.yaml`, `configs/openwrt.yaml` или `configs/linux-systemd.yaml` в приватный каталог на целевом устройстве (например `/root/krm-install/config.yaml`), mode 0600. Не редактируйте tracked template реальными данными.

Заполните:

- `subscriptions.sources`: собственные URL/headers; подписки с секретами доступны только root.
- `targets`: score endpoint и два health endpoint разных hostname/операторов, возвращающих ожидаемый статус; quorum по умолчанию 2. Нельзя использовать один сервер под двумя URL как независимые targets.
- `xray.binary`, `asset_dir`, `config_dir`, `managed_dir`, `base_routing_file`: реальные пути strict JSON; managed_dir должен указывать на тот же каталог, что config_dir, а base_routing_file — на JSON-файл непосредственно в нём.
- `xray.route.inbound_tags` и `replace_outbound_tags`: реальные выбранные tags; убрать `REPLACE_WITH_SELECTED_OUTBOUND`.
- `api.listen: 127.0.0.1:9443`, `api.unix_socket` внутри `paths.run_dir`, API TLS paths/hosts из шаблона.
- `web.enabled: false`: core не раздаёт UI.
- firewall `existing` по умолчанию. `managed` включать только после анализа ownership/mark/table конфликтов, портов/inbounds и LAN интерфейсов. Полный Xray-outage bypass доступен только в managed режиме Linux/OpenWrt; на Keenetic не подтверждён.
- `update.enabled: true` включает периодическую проверку GitHub releases; `auto_apply: false` обязателен. Укажите RC channel, закреплённый `public_key`, приватный `install_dir` и `launcher_socket` внутри `paths.run_dir`. Применение — только явной командой или кнопкой UI. Не переносите ключ из старой версии.

Выберите routing rule без зависимости от названия старого outbound. Из заранее проверенных release assets используйте:

```sh
kee-route-managerctl route-candidates --file /opt/etc/xray/configs/05_routing.json
kee-route-managerctl validate --config /root/krm-install/config.yaml
```

Если ctl ещё не установлен, на доверенном компьютере используйте собранный `dist/kee-route-managerctl` с приватной копией routing JSON или проверенный бинарник из release. `route-candidates` только читает strict JSON и показывает кандидатов; выбор не должен быть автоматическим при неоднозначных rules. Не запускайте непроверенный бинарник для проверки его самого.

## 5. Установить через wizard или готовый config

Выберите интерактивный или prepared config путь и безопасный пароль. Installer читает пароль с терминала; для полностью автоматического запуска создайте root-only `KRM_PASSWORD_FILE` вне Git и удалите после настройки. Не передавайте пароль параметром командной строки.

Новый простой сценарий для Keenetic, OpenWrt и Linux/systemd:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh RELEASE_URL
sh /tmp/krm-bootstrap.sh
```

Без `KRM_MODE` используется `local-ui`. После полной проверки release assets bootstrap запускает подписанный `kee-route-managerctl init-config`, предлагает русский или английский язык, проверяет созданный временный config и только затем передаёт его installer. Нужен интерактивный TTY. Wizard настраивает Xray paths, реальные inbound/outbound tags, subscriptions/headers, score/health targets, размер пула, speed test и update discovery. Routing tags нельзя угадывать; при неоднозначности сначала используйте `route-candidates`. Health targets желательно выбирать на разных hostname у независимых операторов: при quorum 2 один сервер под двумя URL не даёт независимых свидетельств.

Безопасные defaults: firewall `existing`, API на loopback, приватные config/credentials, speed test выключен, автоматическое применение обновлений выключено (`auto_apply: false`). Поиск обновлений — отдельная опция, не разрешение на их применение. При включении speed test нужен HTTPS URL-шаблон с `{bytes}`. Wizard не устанавливает Xray и не угадывает рабочие routing tags.

Advanced/noninteractive сценарий сохраняется и пропускает wizard:

```sh
KRM_MODE=core \
KRM_CONFIG_FILE=/root/krm-install/config.yaml \
sh /tmp/krm-bootstrap.sh
```

После установки Keenetic:

```sh
/opt/bin/kee-route-managerctl ready --config /opt/etc/kee-route-manager/config.yaml
/opt/bin/kee-route-managerctl status --config /opt/etc/kee-route-manager/config.yaml
```

OpenWrt: ctl `/usr/bin/kee-route-managerctl`, config `/etc/kee-route-manager/config.yaml`. Linux/systemd: ctl `/usr/local/bin/kee-route-managerctl`, та же config. Установленные controller config/credentials остаются private (0600, каталоги 0700); URL/headers/пароли не должны попадать в отчёт или логи.

Bootstrap проверяет native Ed25519 signatures manifest и SHA256SUMS, каждый digest/size перед исполнением, затем распаковывает signed install payload. Core-установка получает стабильный launcher и начальный подписанный slot с daemon, UI и CLI; UI в core-only не запускается. Сервис запускает launcher, а он — daemon. На Keenetic helper `/opt/etc/kee-route-manager/xray-status.sh` проверяет именно основной Xray с production config directory, исключая временные probe-процессы. Установщик не перезаписывает существующий config/binary. Readiness endpoint может сообщать **safe degraded**: внимательно прочитать JSON, убедиться в state/reconciliation и фактическом маршруте; HTTP 200 сам по себе не доказывает работающий VPN.

## 6. Core + UI на одном устройстве

Новая интерактивная установка автоматически создаёт **два YAML** — controller и UI:

```sh
KRM_MODE=local-ui sh /tmp/krm-bootstrap.sh
```

Controller config создаётся RU/EN wizard; UI config формируется подписанным CLI с platform-specific defaults. Controller API слушает `127.0.0.1:9443` с TLS; UI использует `https://127.0.0.1:9443`, `insecure_tls: false` и публичный сертификат controller в `controller-ca.crt`. Installer готовит сертификат и trust-файл до запуска UI. Private key остаётся у controller и не копируется в UI. UI config не содержит controller secrets; mode 0644 и каталог 0755 позволяют Linux DynamicUser читать его, ключ UI остаётся 0600. UI trust-файл — `/opt/etc/kee-route-manager-ui/controller-ca.crt` на Keenetic, `/etc/kee-route-manager-ui/controller-ca.crt` на OpenWrt/Linux. Второй YAML вручную не нужен; generated configs проходят validation до запуска сервисов.

Обратная совместимость prepared config сохранена: переданный `KRM_CONFIG_FILE` не запускает wizard. Для `local-ui` также передайте заранее подготовленный UI config из `configs/ui-keenetic.yaml` или `configs/ui-linux-openwrt.yaml`, согласованный с controller TLS:

```sh
KRM_MODE=local-ui \
KRM_CONFIG_FILE=/root/krm-install/config.yaml \
KRM_UI_CONFIG_FILE=/root/krm-install/ui.yaml \
sh /tmp/krm-bootstrap.sh
```

На Keenetic/OpenWrt один сервис launcher запускает daemon и локальный UI; отдельный `S98kee-route-manager-ui`/UI procd-сервис не устанавливается. На Linux UI сохраняет отдельный DynamicUser-сервис и обновляется вручную; launcher не запускает его с правами root. UI не хранит admin пароль и не получает private API key. Existing installation нельзя молча перезаписать; перед reinstall следуйте разделу 10.

Откройте `https://ROUTER_ADDRESS:9444/`. Сверьте SHA256 fingerprint сертификата через SSH, затем добавьте его в доверенные браузера либо используйте собственный trusted cert. Не отключайте TLS проверки. Login/session/CSRF обрабатываются core. Проверить `/`, `/assets/app.css`, `/assets/app.js`, `/sw.js`, `/manifest.webmanifest`: HTTP 200; PWA на доверенном HTTPS origin.

## 7. UI на другом компьютере

Core API слушает только loopback. Используйте постоянный SSH tunnel с ограниченным forwarding account/key на роутере. Не открывайте порт root/API на WAN. Пример в отдельной сессии на UI-компьютере:

```sh
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -L 127.0.0.1:9445:127.0.0.1:9443 -p SSH_PORT USER@ROUTER_ADDRESS
```

SSH tunnel шифрует межмашинный участок и аутентифицирует SSH peer; проверка HTTPS controller остаётся обязательной. В UI YAML используйте `ui.upstream: https://127.0.0.1:9445`, `insecure_tls: false`, `upstream_ca_file: /etc/kee-route-manager-ui/controller-ca.crt` на Linux/OpenWrt или `/opt/etc/kee-route-manager-ui/controller-ca.crt` на Keenetic. По аутентифицированному SSH каналу получите **публичный** controller certificate/CA и передайте его через `KRM_UPSTREAM_CA_FILE`; private key не переносите. Сертификат должен быть доверенным и подходить для loopback HTTPS upstream; не отключайте проверку имени/цепочки. При необходимости дополнительно задайте `ui.upstream_spki_sha256`.

Installer проверяет соответствие CA path и валидность сертификата до записи системных файлов. Не открывайте controller API напрямую в LAN/WAN. Не устанавливайте `insecure_tls: true`, не отключайте SSH host verification.

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh RELEASE_URL
KRM_MODE=ui \
KRM_CONFIG_FILE=/root/krm-install/ui.yaml \
KRM_UPSTREAM_CA_FILE=/root/krm-install/controller-ca.crt \
sh /tmp/krm-bootstrap.sh
```

UI-only режим не использует controller wizard: `KRM_MODE=ui` требует заранее подготовленный `KRM_CONFIG_FILE` с `instance.role: ui`. Без trusted upstream CA и authenticated SSH flow удалённую UI-установку не продолжать.

Скачивание выше выполнять на Linux-компьютере с UI. Для UI на OpenWrt/Keenetic выбрать bootstrap соответствующей платформы, не переносить Keenetic bootstrap на Linux-хост.

UI Linux service использует DynamicUser и собственный state dir. SSH tunnel обеспечить отдельным сервисом пользователя/администратора и проверить reconnect/reboot. При падении tunnel UI возвращает upstream unavailable; маршрутизацией продолжает владеть core.

## 8. Проверить и принять

Проверки выполнять с учётом [ограничений платформ](KNOWN_LIMITATIONS.md), сохраняя независимый доступ по SSH. Минимум:

```sh
kee-route-managerctl benchmark --config CONFIG_PATH
kee-route-managerctl status --config CONFIG_PATH
kee-route-managerctl switch --slot 1 --config CONFIG_PATH
```

Для UI дополнительно `kee-route-manager-ui ready --config UI_CONFIG_PATH`: проверяет собственный HTTPS и ответ core через proxy.

Реальный slot выбрать из status; пустой slot не переключать. Benchmark возвращает operation ID; дождаться операции и появления pool. Попытка запуска второго daemon должна завершиться ошибкой ownership до запуска scheduler. Проверить direct/restore только после анализа платформенных capabilities. Отказ одного health target не должен переводить VPN в direct. Для Keenetic Xray outage пока требует документированного ручного восстановления собственной interception конфигурации, с независимым recovery доступом. Не пытаться угадать недокументированные команды отключения firewall.

## 9. Проверка и применение обновлений

Проверка выполняется автоматически с `update.check_interval`, применение — только по явному запросу:

```sh
kee-route-managerctl update-check --config CONFIG_PATH
kee-route-managerctl update-status --config CONFIG_PATH
kee-route-managerctl update-apply --target-version 1.1.0-rc.7 --config CONFIG_PATH
```

Замените target version на доступную проверенную версию из `update-check`. Указание версии защищает от смены выбранного релиза между проверкой и применением. Кнопка UI использует тот же authenticated API; `update-status` показывает результат и после перезапуска daemon. Проверяйте результат, readiness и фактический маршрут после завершения операции.

Launcher скачивает и проверяет все три компонента, останавливает текущие дочерние процессы после подготовки daemon, запускает candidate в trial и проверяет PID/version/nonce, reconciliation, API и управляемый UI. До commit неудачный trial возвращает прежнюю версию. После commit перезапуск выполняется уже на новой версии: старый controller state не восстанавливается. Стабильный launcher и отдельный UI на Linux/другом хосте обновляются вручную. `auto_apply: true` не поддерживается.

Переход с установки без launcher выполняйте как restore → остановка/удаление → backup сохранённых каталогов → чистая signed установка. Прямой импорт предназначен для остановленных сервисов и заранее проверенных assets:

```sh
kee-route-manager-launcher install --config CONFIG_PATH --release-dir VERIFIED_DIST
# Только Keenetic/OpenWrt для совместного локального UI:
kee-route-manager-launcher install --config CONFIG_PATH --ui-config UI_CONFIG_PATH --release-dir VERIFIED_DIST
```

Это альтернативы для одноразового первоначального импорта, а не две последовательные команды. Старые standalone UI init-скрипты необходимо удалить до совместной установки. `serve --config CONFIG_PATH` читает сохранённый UI config из записи launcher; ручной параллельный запуск второго parent не допускается.

## 10. Удаление, reinstall и rollback

Сначала backup. Пока daemon работает:

```sh
kee-route-managerctl restore-xray --config CONFIG_PATH
```

Используйте signed `install/PLATFORM/uninstall.sh` из payload. Он также выполняет restore через живой daemon **до** остановки. Успешный restore сохраняет `automatic_routing_paused: true`: scheduler не устанавливает маршрутизацию заново, в том числе после перезапуска daemon. Возобновление требует успешного ручного `benchmark`; не запускайте его во время удаления. Прежние опубликованные assets этим исправлением не обновляются. Ошибка restore оставляет сервисы и файлы для диагностики. По умолчанию приватный config/state сохраняются; `--purge` удаляет только стандартные KRM config/state/cache/update-slot directories. Нестандартные пути проверять и очищать отдельно.

Укажите режим установки явно:

```sh
KRM_MODE=core sh install/PLATFORM/uninstall.sh
# Или core + local UI:
KRM_MODE=local-ui sh install/PLATFORM/uninstall.sh
# На отдельном UI-хосте (без локального ctl/restore):
KRM_MODE=ui sh install/PLATFORM/uninstall.sh
```

Заменить `PLATFORM` на `keenetic`, `openwrt` или `linux-systemd`. Для UI-only предварительный `restore-xray` на UI-хосте пропустить: удаление UI не меняет маршрутизацию удалённого core. `--purge` добавлять только после backup и осознанного выбора удаления сохранённых данных.

Перед reinstall перенесите сохранённые KRM config/state/cache в приватный backup либо выполните осознанный `--purge`. Проверьте Xray strict JSON и WAN, затем повторите чистую установку. При routing drift восстановление должно отказаться от удаления пользовательских правок; разберите diff вручную с резервной копией, не возвращайте вслепую весь старый routing файл.

Для ручного возврата после committed update: restore/uninstall новой версии → проверить Xray/WAN → установить заранее сохранённую **проверенную signed** версию и совместимый config с нуля. Не переключайте `current` вручную и не возвращайте старый state поверх нового. Совместимость state/config старых версий не гарантируется. Не копируйте новый binary поверх работающего daemon. Автоматический rollback launcher ограничен trial до commit.

## 11. Отчёт

Версия/tag/SHA256 assets, платформа/архитектура/Xray version, mode и сервисы, status/readiness без секретов, installation/login/PWA/benchmark/ownership/failure/reboot/restore/reinstall outcomes, остающиеся limitations. Никогда не публиковать backup, subscriptions, UUID/Reality data или private IP topology.

## 12. v1.2.0 settings/address acceptance

For new interactive local-ui installations, enter the desired benchmark interval/cache policy, then explicitly choose loopback or a private LAN IPv4, HTTPS port and optional DNS name. The default127.0.0.1 restricts access to the device/tunnel; it is not a usable LAN address on another computer. The wizard prints a local DNS record requirement and puts the name in the UI certificate SAN; CLI never mutates router DNS. Prepared config skips the wizard and retains its values.

For an existing installation, use the signed launcher/application maintenance procedure rather than running a fresh installer over its record. Address editing requires the separately installed v1.2.0+ launcher. Back up controller/UI YAML, all UI certificates and private authorization/source/launcher metadata outside Git; retain SSH while changing UI. Preserve the existing listen IP/domain/port and benchmark/cache policy unless explicitly asked to change them.

Verify Router → Settings loads the actual values and validate/no-op do not change YAML or scheduler. A real settings change must return202, complete as the same revision, retain valid sessions/Xray route/process and expose positive readiness. Check failed initialization rollback with isolated fixtures; do not inject faulty router/Xray settings on live hardware. Settings reload intentionally resets scheduler phase/history without changing the saved interval or starting another startup benchmark.

Panel addressing: prepare a free local port/name, obtain the public certificate if it changed, apply, verify both the trial URL and original access, then explicitly confirm within five minutes. An unconfirmed trial must restore the original UI; prepare/apply must reject reserved/occupied ports and foreign IPs. No WAN rules are added. On Keenetic keep native/external configuration editors idle during an absent DNS alias addition; observed unsaved/drift state must refuse the write. Do not claim a power-loss or every-client hardware qualification from API/TLS checks alone.
