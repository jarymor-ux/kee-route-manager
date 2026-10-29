# Полная установка Kee Route Manager для AI-агента

Этот файл — инструкция от ссылки на репозиторий до проверенной установки **v2.0.3** (stable channel), собираемой Go 1.27.3. Прочитайте также корневой `AGENTS.md`. До начала установки подтвердите фактическую публикацию полного подписанного набора v2.0.3; отсутствие релиза/asset означает остановку, а не установку с main или старой версии. Все модели остаются experimental до аппаратной приёмки; контейнерные проверки её не заменяют. Старые assets неизменны. Stable-сценарий v2.0.3 требует существующий рабочий Xray и не устанавливает Entware/XKeen/Xray; изменения RC 2.1.0-rc.4 описаны отдельно ниже.

**Прежний v1.0.0-rc.2 не использовать для новой установки:** его раздельные секции Xray `routing` перезаписывают друг друга. В новой версии routing объединён в выбранном base-файле и проверен с реальным Xray. Прежние опубликованные assets остаются неизменными.

**Scope:** только Keenetic/Netcraze на совместимой стоковой KeeneticOS/NDMS; отдельный PC допускается только как management UI. Начните с [матрицы моделей, ABI, RAM и полного пути KN-2410](HARDWARE.md). В 2.0.1 добавлен MIPS BE; KN-2410/2510 остаются экспериментальными до аппаратной приёмки. Старый v1.3.3 не имеет BE assets. Перед обновлением старого generated Keenetic YAML обязательна [офлайн-миграция](BREAKING_CHANGES.md#201-keeneticnetcraze-only-router-targets).

## Изменения установщика в RC 2.1.0-rc.4

Следующий сценарий относится к RC 2.1.0-rc.4. Сначала подтвердите фактическую публикацию полного подписанного immutable набора; без него не устанавливать. Он не меняет опубликованные assets v2.0.3 и не разрешает устанавливать с ветки. Подробный контракт — [автоматизация установки](INSTALLER_AUTOMATION_PLAN.md).

- Entware и доверенные инструменты (`curl`, CA/OpenSSL, `tar`, `unzip`, `jq`, `flock`, `timeout`, `ln`/`mv` с точной no-target-directory семантикой, native `ip`/iptables/ipset/conntrack и process utilities) устанавливаются вручную. При чистой системе wizard предлагает закреплённые XKeen 2.1 / Xray v26.10.21, проверяет размер/SHA-256/ABI и отдельно спрашивает согласие на активацию. Существующие конфликтующие сервисы, hooks, процессы и cron требуют ручного разбора до размещения executable.
- Чистый Xray получает loopback inbounds и исходный blackhole-маршрут: до успешного бенчмарка VPN-трафик блокируется. Начальный профиль уменьшает pool/batch/workers/finalists до2 и отключает speed test. Автоматическое создание Entware, DNS, WAN-политик, назначение клиентов и форматирование отсутствуют. Чистый native-профиль перехватывает только IPv4; существующий IPv6 остаётся у оператора, без заявления о защите от IPv6-утечек и без отключения IPv6 в ядре.
- До активации в штатном UI роутера должна существовать ровно одна политика `xkeen` с ненулевой mark. Для firmware с RCI-авторизацией подготовьте root-only JSON с единственным полем `rci_token` вне Git и передайте путь через `KRM_XKEEN_CONFIG_FILE`; токен не передавайте аргументом CLI. Недоступная политика/RCI оставляет native файлы неактивными. Для повторной попытки после этого можно повторить wizard: перед повторным использованием согласия проверяются native receipt, sibling links и неизменённый executable/config payload; изменённый токен обновляйте в приватном `/opt/etc/xkeen/xkeen.json` явно, без перезаписи чужих настроек.
- Сервис запускается синхронно через проверяющую обёртку, без upstream `xkeen -i`/opkg self-heal. DSCP-перехват и `xkeen_full` выключены; при исчезновении policy mark fallback на всех клиентов запрещён. После отдельного согласия root-only receipt разрешает такой же защищённый запуск после reboot. Прочие native/config writers должны быть остановлены на время активации.
- Browser UI по умолчанию HTTP на выбранном private LAN/loopback; пароль и cookies не шифруются. Предупреждение wizard обязательно, HTTPS остаётся доступным в prepared config. Upstream контроллера — проверенный loopback HTTPS. Stable update checks включены по умолчанию, auto-apply выключен.

У прерванной установки три независимых журнала. До запуска/launcher import KRM откатывает только совпадающие собственные static files; после этого сохраняет состояние и разрешает проверенное продолжение. Используйте **тот же** проверенный immutable bootstrap и исходный mode, например:

```sh
KRM_MODE=local-ui KRM_INSTALL_ACTION=recover sh /tmp/krm-bootstrap.sh
KRM_MODE=local-ui KRM_INSTALL_ACTION=resume sh /tmp/krm-bootstrap.sh
```

Это альтернативы по фазе журнала, не команды для последовательного слепого выполнения. После launcher/start intent `recover` откажет и укажет `resume`; исходные временные YAML/пароль для resume не нужны. Содержимое/права/идентичности должны совпадать; при drift нужен приватный разбор, не удаление журнала. До launcher intent восстановите partial static publication через `recover`, затем повторите новую установку. Резервные snapshot bytes и metadata хранятся в приватном журнале, чужие файлы не перезаписываются.

Для незавершённой публикации зависимости из аутентифицированного payload: `sh install/common/dependency-provision.sh xray --recover` (либо `xkeen`). Для native setup: `sh install/common/native-bootstrap.sh /absolute/unused-stage --recover`; исходный stage при recovery не нужен. Совпадающая полная публикация подтверждается, partial inactive publication откатывается; изменённые данные сохраняются. Уже завершённые native зависимости не удаляются при ошибке установки KRM или его uninstall. Для полного удаления native interception сначала отдельно переведите клиентов с политики `xkeen` и проверьте WAN/recovery; процедура KRM restore/uninstall ниже не является uninstall XKeen. Не удаляйте native directories или hooks вслепую.

Аппаратная приёмка чистого сценария, UI/LAN, reboot, power-loss и восстановления остаётся невыполненной; RC остаётся экспериментальным; аппаратная приёмка не заявляется.

## 1. Получить входные данные и подключиться

Нужны адрес Keenetic/Netcraze (или отдельного UI-компьютера), SSH-порт, пользователь с административными правами, SSH-ключ/доступ через агент, подтверждённый fingerprint, свободное место, выбранный режим, приватные подписки и минимум два независимых health URL. Секреты запрашивать безопасным способом, не писать в Git/отчёт/аргументы процессов. Не угадывать SSH-порт: административный SSH Keenetic и Linux-shell Entware — разные среды; Entware часто использует 222.

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

Пути примера относятся к типичной установке XKeen на Entware; проверьте фактические пути и API-команды установленной версии Xray. Используется только `platform.kind: keenetic`; interception остаётся у оператора/XKeen, отдельного поля firewall-mode нет; не устанавливать KRM поверх неизвестного scheduler. В опубликованном v2.0.3 KRM не устанавливает Entware/XKeen/Xray автоматически.

Проверьте строгий JSON во всех файлах `*.json`, единственную пользовательскую секцию `routing` в выбранном base-файле, фактический asset directory и семантику команд XKeen. Перезапуск должен завершаться синхронно, а status возвращать ошибку при отсутствии основного Xray; временные процессы benchmark не подтверждают его работу. Подробности настройки — в [CONFIGURATION.md](CONFIGURATION.md).

Официальная инструкция [Entware для Keenetic](https://support.keenetic.com/eu/titan/kn-1812/en/20980-installing-the-entware-repository-on-a-usb-drive.html) объясняет подготовку USB/OPKG и отдельный порт SSH. OpenSSL должен поддерживать [Ed25519 через pkeyutl](https://docs.openssl.org/4.0/man1/openssl-pkeyutl/). Если доверенного OpenSSL/curl/CA нет, сначала установить их пакетным менеджером платформы; bootstrap завершается до исполнения скачанных программ.

## 2. Инвентаризация и приватная резервная копия

Проверьте процессы, Entware init, cron, Xray routing, firewall/policy rules. Сохраните резервную копию вне репозитория, каталог mode 0700, архив mode 0600. На роутере оставьте независимую SSH-сессию и рабочий путь восстановления.

```sh
ps w
ls /opt/etc/init.d /etc/init.d 2>/dev/null
crontab -l
```

При переходе с прежнего проекта найдите его фактические пути/процессы на устройстве, остановите scheduler/watchdog, восстановите routing и WAN, проверьте Xray. Удаляйте только идентифицированные файлы. Инвентаризация и удаление старой установки выполняются на аппаратном этапе с владельцем, а не на компьютере разработки.

**Безопасность старой версии:** v1.3.3 собран Go 1.27.2 и затронут [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809). Сборка 2.0.3 использует исправленный Go 1.27.3; устанавливать только после фактической публикации и проверки нового подписанного набора. Старые binaries не исправляются и не заменяются; не подменяйте доверенную подпись изменяемой веткой.

## 3. Получить одну фиксированную версию

На компьютере агента:

```sh
git clone https://github.com/jarymor-ux/kee-route-manager.git
cd kee-route-manager
git checkout RELEASE_TAG
```

Читайте release notes и `internal/releasetrust/public.key`. Для этой версии используется закреплённый release signing key; ключ доверять через аутентифицированный репозиторий/канал владельца, а не через файл, скачанный вместе с потенциально подменённой подписью. Bootstrap содержит pinned public key. Первое получение bootstrap требует отдельной проверки до `sh`: на доверенном компьютере скачайте полный набор assets одного релиза и проверьте обе Ed25519 подписи и каждый digest командой ниже. Сам скрипт проверки должен быть из доверенного checkout, а public key — независимо подтверждён владельцем; ключ рядом с подписью сам по себе не устанавливает доверие.

```sh
KRM_RELEASE_PUBLIC_KEY=/TRUSTED/release-public.key \
python3 scripts/verify-release.py /PRIVATE/complete-release
```

Выполнять bootstrap можно только после успешной проверки подписанного inventory, включая его собственный digest; перенесите проверенные bytes на роутер по аутентифицированному SSH. При ошибке остановитесь.

Замените `RELEASE_TAG` на неизменяемый tag выбранного релиза с интерактивным установщиком. `RELEASE_URL` ниже — полный HTTPS URL bootstrap asset той же версии: `bootstrap-keenetic.sh` для Keenetic/Netcraze. Сначала убедитесь, что опубликован весь подписанный набор. Если release ещё отсутствует, установка по этим ссылкам должна остановиться, а не подменять версию. Не используйте `main` или `/releases/latest` для установки. Версия, бинарники, сервисы и конфигурации должны совпадать.

## 4. Выбрать подготовленную или интерактивную конфигурацию

При новой установке без `KRM_CONFIG_FILE` приватный config создаёт RU/EN wizard после полной проверки signed assets. Подготовка YAML вручную необязательна; предварительно определите реальные Xray paths и routing tags, subscriptions и независимые health targets. Для prepared/noninteractive пути выполните подготовку ниже: если передан `KRM_CONFIG_FILE`, wizard не запускается.

Скопируйте подходящий шаблон `configs/keenetic.yaml` в приватный каталог на целевом устройстве (например `/root/krm-install/config.yaml`), mode 0600. Не редактируйте tracked template реальными данными.

Заполните:

- `subscriptions.sources`: собственные URL/headers; подписки с секретами доступны только root.
- `targets`: score endpoint и два health endpoint разных hostname/операторов, возвращающих ожидаемый статус; quorum по умолчанию 2. Нельзя использовать один сервер под двумя URL как независимые targets.
- `xray.binary`, `asset_dir`, `config_dir`, `managed_dir`, `base_routing_file`: реальные пути strict JSON; managed_dir должен указывать на тот же каталог, что config_dir, а base_routing_file — на JSON-файл непосредственно в нём.
- `xray.route.inbound_tags` и `replace_outbound_tags`: реальные выбранные tags; убрать `REPLACE_WITH_SELECTED_OUTBOUND`.
- `api.listen: 127.0.0.1:9443`, `api.unix_socket` внутри `paths.run_dir`, API TLS paths/hosts из шаблона.
- `web.enabled: false`: core не раздаёт UI.
- interception принадлежит оператору/XKeen; отдельный режим KRM-managed firewall не предлагается. Автоматический direct bypass при полной остановке Xray на Keenetic/Netcraze не поддерживается. Сохраните независимый SSH/recovery доступ.
- `update.enabled: true` включает периодическую проверку GitHub releases; `auto_apply: false` обязателен. Укажите выбранный канал (`stable` по умолчанию в новом wizard), закреплённый `public_key`, приватный `install_dir` и `launcher_socket` внутри `paths.run_dir`. Применение — только явной командой или кнопкой UI. Не переносите ключ из старой версии.

Выберите routing rule без зависимости от названия старого outbound. Из заранее проверенных release assets используйте:

```sh
kee-route-managerctl route-candidates --file /opt/etc/xray/configs/05_routing.json
kee-route-managerctl validate --config /root/krm-install/config.yaml
```

Если ctl ещё не установлен, на доверенном компьютере используйте собранный `dist/kee-route-managerctl` с приватной копией routing JSON или проверенный бинарник из release. `route-candidates` только читает strict JSON и показывает кандидатов; выбор не должен быть автоматическим при неоднозначных rules. Не запускайте непроверенный бинарник для проверки его самого.

## 5. Установить через wizard или готовый config

Выберите интерактивный или prepared config путь и безопасный пароль. Installer читает пароль с терминала; для полностью автоматического запуска создайте root-only `KRM_PASSWORD_FILE` вне Git и удалите после настройки. Не передавайте пароль параметром командной строки.

Новый простой сценарий для Keenetic/Netcraze:

```sh
curl --proto '=https' -fsSLo /tmp/krm-bootstrap.sh RELEASE_URL
sh /tmp/krm-bootstrap.sh
```

Без `KRM_MODE` используется `local-ui`. После полной проверки release assets bootstrap запускает подписанный `kee-route-managerctl init-config`, предлагает русский или английский язык, проверяет созданный временный config и только затем передаёт его installer. Нужен интерактивный TTY. Wizard настраивает Xray paths, реальные inbound/outbound tags, subscriptions/headers, score/health targets, размер пула, speed test и update discovery. Routing tags нельзя угадывать; при неоднозначности сначала используйте `route-candidates`. Health targets желательно выбирать на разных hostname у независимых операторов: при quorum 2 один сервер под двумя URL не даёт независимых свидетельств.

Безопасные defaults: firewall `existing`, API на loopback, приватные config/credentials, speed test выключен, автоматическое применение обновлений выключено (`auto_apply: false`). Поиск обновлений — отдельная опция, не разрешение на их применение. При включении speed test нужен HTTPS URL-шаблон с `{bytes}`. В опубликованном v2.0.3 wizard не устанавливает XKeen/Xray. На существующей конфигурации он автоматически выбирает только один однозначный безусловный VPN-маршрут; несколько кандидатов требуют явного выбора. На чистой системе без Xray настройка зависимостей пока остаётся ручной.

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

Те же `/opt` пути и единый адаптер применяются к совместимому Netcraze. Установленные controller config/credentials остаются private (0600, каталоги 0700); URL/headers/пароли не должны попадать в отчёт или логи.

Bootstrap проверяет native Ed25519 signatures manifest и SHA256SUMS, каждый digest/size перед исполнением, затем распаковывает signed install payload. Core-установка получает стабильный launcher и начальный подписанный slot с daemon, UI и CLI; UI в core-only не запускается. Сервис запускает launcher, а он — daemon. На Keenetic helper `/opt/etc/kee-route-manager/xray-status.sh` проверяет именно основной Xray с production config directory, исключая временные probe-процессы. Установщик не перезаписывает существующий config/binary. Readiness endpoint может сообщать **safe degraded**: внимательно прочитать JSON, убедиться в state/reconciliation и фактическом маршруте; HTTP 200 сам по себе не доказывает работающий VPN.

## 6. Core + UI на одном устройстве

Новая интерактивная установка автоматически создаёт **два YAML** — controller и UI:

```sh
KRM_MODE=local-ui sh /tmp/krm-bootstrap.sh
```

Controller config создаётся RU/EN wizard; UI config формируется подписанным CLI с Entware defaults. Controller API слушает `127.0.0.1:9443` с TLS; UI использует `https://127.0.0.1:9443`, `insecure_tls: false` и публичный сертификат controller в `controller-ca.crt`. Installer готовит сертификат и trust-файл до запуска UI. Private key остаётся у controller и не копируется в UI. UI config не содержит controller secrets; trust-файл — `/opt/etc/kee-route-manager-ui/controller-ca.crt`. Второй YAML вручную не нужен; generated configs проходят validation до запуска сервисов.

Обратная совместимость prepared config сохранена: переданный `KRM_CONFIG_FILE` не запускает wizard. Для `local-ui` также передайте заранее подготовленный UI config из `configs/ui-keenetic.yaml`, согласованный с controller TLS:

```sh
KRM_MODE=local-ui \
KRM_CONFIG_FILE=/root/krm-install/config.yaml \
KRM_UI_CONFIG_FILE=/root/krm-install/ui.yaml \
sh /tmp/krm-bootstrap.sh
```

На Keenetic/Netcraze один Entware сервис launcher запускает daemon и локальный UI; отдельный `S98kee-route-manager-ui` для local-ui не устанавливается. Отдельный UI управления на другом компьютере обслуживается вручную от непривилегированного пользователя. UI не хранит admin пароль и не получает private API key. Existing installation нельзя молча перезаписать; перед reinstall следуйте разделу 10.

Для новой configless local-ui установки мастер по умолчанию использует **HTTP** на выбранном приватном LAN IPv4-адресе (или loopback). При единственном активном приватном адресе bridge-интерфейса этот адрес предлагается по умолчанию; при неоднозначности предложен `127.0.0.1`, который недоступен с другого устройства. Проверяйте фактический bind-адрес в итоговом YAML. Откройте `http://ROUTER_ADDRESS:9444/` только из доверенной LAN. **HTTP передаёт пароль и session cookie без шифрования**; не открывайте этот порт на WAN, не используйте недоверенные Wi-Fi и клиентские сети. HTTPS по-прежнему поддерживается в заранее подготовленном UI YAML и требует доверенного сертификата. Внутренний upstream между UI и controller остаётся проверенным HTTPS на loopback. Login/session/CSRF обрабатываются core. Проверьте `/`, `/assets/app.css`, `/assets/app.js`, `/sw.js`, `/manifest.webmanifest`: HTTP 200. Возможности PWA и service worker требуют secure context (обычно HTTPS или localhost) и могут быть недоступны при обращении по HTTP LAN.

## 7. UI на другом компьютере

Core API слушает только loopback. Используйте постоянный SSH tunnel с ограниченным forwarding account/key на роутере. Не открывайте порт root/API на WAN. Пример в отдельной сессии на UI-компьютере:

```sh
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -L 127.0.0.1:9445:127.0.0.1:9443 -p SSH_PORT USER@ROUTER_ADDRESS
```

SSH tunnel шифрует межмашинный участок и аутентифицирует SSH peer; проверка HTTPS controller остаётся обязательной. В UI YAML используйте `ui.upstream: https://127.0.0.1:9445`, `insecure_tls: false`, `upstream_ca_file` с абсолютным путём к публичному controller CA на UI-хосте. По аутентифицированному SSH каналу получите **публичный** controller certificate/CA; private key не переносите. Сертификат должен подходить для loopback HTTPS upstream. Дополнительно можно задать `ui.upstream_spki_sha256`. Не отключайте проверку имени/цепочки или SSH host verification.

На отдельном компьютере нет KRM Core, generic router installer или системного service template. Из одного неизменяемого релиза скачайте native UI binary и подписанные manifest/checksum файлы; до запуска проверьте обе Ed25519 подписи доверенным закреплённым ключом, версию/канал и digest/size UI, как в [release procedure](RELEASE.md). Не запускайте Keenetic bootstrap на PC. Создайте отдельного непривилегированного пользователя и приватные writable каталоги UI; подготовьте YAML с `instance.role: ui`, `api.enabled: false`, `ui.enabled: true`, `web.enabled: true`, HTTPS browser-facing listener только на нужном loopback/private адресе и указанным verified upstream. За основу структуры можно взять `configs/ui-keenetic.yaml`, заменив **все** `/opt` paths на каталоги UI-пользователя и upstream на TLS tunnel. Controller YAML и private state не копировать.

Проверьте подготовленный конфиг заранее проверенным CLI той же версии и запустите проверенный native UI binary:

```sh
kee-route-managerctl validate --config /ABSOLUTE/PRIVATE/ui.yaml
kee-route-manager-ui serve --config /ABSOLUTE/PRIVATE/ui.yaml
```

Это ручной management-only сценарий. Нужные директории, права и доверенный публичный CA должны существовать до запуска. UI binary не управляет Entware, firewall или Xray. Администратор UI-хоста обеспечивает supervisor и tunnel reconnect; KRM не устанавливает службы PC. Проверьте reconnect/reboot UI-хоста. При падении tunnel UI возвращает upstream unavailable, маршрутизацией продолжает владеть единственный core на роутере. UI-only на поддерживаемом Keenetic/Netcraze по-прежнему можно установить его подписанным wrapper с `KRM_MODE=ui`, подготовленным UI YAML и `KRM_UPSTREAM_CA_FILE`.

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

Launcher скачивает и проверяет все три компонента, останавливает текущие дочерние процессы после подготовки daemon, запускает candidate в trial и проверяет PID/version/nonce, reconciliation, API и управляемый UI. До commit неудачный trial возвращает прежнюю версию. После commit перезапуск выполняется уже на новой версии: старый controller state не восстанавливается. Стабильный launcher и отдельный удалённый UI обновляются вручную. `auto_apply: true` не поддерживается.

Переход с установки без launcher выполняйте как restore → остановка/удаление → backup сохранённых каталогов → чистая signed установка. Прямой импорт предназначен для остановленных сервисов и заранее проверенных assets:

```sh
kee-route-manager-launcher install --config CONFIG_PATH --release-dir VERIFIED_DIST
# Keenetic/Netcraze для совместного локального UI:
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
# UI-only на отдельном поддерживаемом роутере (без локального ctl/restore):
KRM_MODE=ui sh install/PLATFORM/uninstall.sh
```

Заменить `PLATFORM` на `keenetic` (единый адаптер Keenetic/Netcraze). Для UI-only предварительный `restore-xray` на UI-хосте пропустить: удаление UI не меняет маршрутизацию удалённого core. На PC остановите вручную настроенный UI/tunnel supervisor и удалите только принадлежащие UI файлы, не запускайте роутерный uninstall. `--purge` добавлять только после backup и осознанного выбора удаления сохранённых данных.

Перед reinstall перенесите сохранённые KRM config/state/cache в приватный backup либо выполните осознанный `--purge`. Проверьте Xray strict JSON и WAN, затем повторите чистую установку. При routing drift восстановление должно отказаться от удаления пользовательских правок; разберите diff вручную с резервной копией, не возвращайте вслепую весь старый routing файл.

Для ручного возврата после committed update: restore/uninstall новой версии → проверить Xray/WAN → установить заранее сохранённую **проверенную signed** версию и совместимый config с нуля. Не переключайте `current` вручную и не возвращайте старый state поверх нового. Совместимость state/config старых версий не гарантируется. Не копируйте новый binary поверх работающего daemon. Автоматический rollback launcher ограничен trial до commit.

## 11. Отчёт

Версия/tag/SHA256 assets, платформа/архитектура/Xray version, mode и сервисы, status/readiness без секретов, installation/login/PWA/benchmark/ownership/failure/reboot/restore/reinstall outcomes, остающиеся limitations. Никогда не публиковать backup, subscriptions, UUID/Reality data или private IP topology.

## 12. v1.2.0 settings/address acceptance

For new interactive local-ui installations, enter the desired benchmark interval/cache policy, then explicitly choose loopback or a private LAN IPv4, HTTPS port and optional DNS name. The default127.0.0.1 restricts access to the device/tunnel; it is not a usable LAN address on another computer. The wizard prints a local DNS record requirement and puts the name in the UI certificate SAN; CLI never mutates router DNS. Prepared config skips the wizard and retains its values.

For an existing installation, use the signed launcher/application maintenance procedure rather than running a fresh installer over its record. Address editing requires the separately installed v1.2.0+ launcher. Back up controller/UI YAML, all UI certificates and private authorization/source/launcher metadata outside Git; retain SSH while changing UI. Preserve the existing listen IP/domain/port and benchmark/cache policy unless explicitly asked to change them.

Verify Router → Settings loads the actual values and validate/no-op do not change YAML or scheduler. A real settings change must return202, complete as the same revision, retain valid sessions/Xray route/process and expose positive readiness. Check failed initialization rollback with isolated fixtures; do not inject faulty router/Xray settings on live hardware. Settings reload intentionally resets scheduler phase/history without changing the saved interval or starting another startup benchmark.

Panel addressing: prepare a free local port/name, obtain the public certificate if it changed, apply, verify both the trial URL and original access, then explicitly confirm within five minutes. An unconfirmed trial must restore the original UI; prepare/apply must reject reserved/occupied ports and foreign IPs. No WAN rules are added. On Keenetic keep native/external configuration editors idle during an absent DNS alias addition; observed unsaved/drift state must refuse the write. Do not claim a power-loss or every-client hardware qualification from API/TLS checks alone.
