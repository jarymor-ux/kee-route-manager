'use strict';

let settingsSnapshot = null;
let settingsRevision = '';
let settingsPoolSize = 0;
let settingsDirty = false;
let settingsLoading = false;
let settingsSaving = false;
let settingsChecking = false;
let settingsGeneration = 0;
let settingsApplyTimer;
let settingsApplyAttempts = 0;
let settingsTargetRevision = '';
let settingsNeedsRefresh = false;
let settingsApplyDeadline = 0;

// Only this allowlist is editable; credentials, listeners, TLS and routing stay private.
const settingsSections = [
  ['Тестирование', 'benchmark', [
    ['full_interval', 'Интервал полного теста', 'duration', 'Например: 5m, 1h.'],
    ['batch_size', 'Узлов в пакете', 'number'], ['latency_workers', 'Параллельных проверок задержки', 'number'],
    ['requests_per_weight', 'Запросов на единицу веса', 'number'], ['finalists', 'Финалистов', 'number'],
    ['min_improvement_percent', 'Минимальное улучшение, %', 'number'],
    ['switch_cooldown', 'Пауза между переключениями', 'duration'],
    ['stability_before_upgrade', 'Стабильность перед улучшением маршрута', 'duration'],
  ]],
  ['Измерение скорости', 'benchmark.speed', [
    ['enabled', 'Измерять скорость', 'boolean'], ['workers', 'Параллельных измерений', 'number'],
    ['url_template', 'Шаблон URL измерения', 'text', 'Используйте URL публичного сервиса без паролей и токенов.'],
    ['warmup_bytes', 'Объём разогрева', 'bytes'], ['min_sample_bytes', 'Минимальный объём', 'bytes'],
    ['max_sample_bytes', 'Максимальный объём', 'bytes'], ['target_duration', 'Длительность измерения', 'duration'],
    ['repetitions', 'Повторов', 'number'],
  ]],
  ['Проверка связи', 'health', [
    ['recovery_threshold', 'Успехов для восстановления', 'number'], ['request_timeout', 'Таймаут запроса', 'duration'],
    ['max_response_bytes', 'Максимальный размер ответа', 'bytes'], ['hot_pool_freshness', 'Свежесть горячего пула', 'duration'],
    ['provider_retry_backoff', 'Паузы повторных запросов подписки', 'durations', 'Через запятую, например: 30s, 1m, 5m.'],
  ]],
  ['Резервное переключение', 'failover', [
    ['detection_interval', 'Интервал обнаружения отказа', 'duration'], ['failure_threshold', 'Ошибок до переключения', 'number'],
    ['probe_timeout', 'Таймаут одной проверки', 'duration'], ['overall_deadline', 'Общий лимит проверки', 'duration'],
    ['quorum', 'Необходимых успешных проверок', 'number'],
  ]],
  ['Загрузка подписок', 'subscriptions', [
    ['cache_enabled', 'Использовать кэш подписок', 'boolean', 'При выключении каждое тестирование скачивает подписки заново; фоновое обновление отключено.'],
    ['cache_ttl', 'Срок хранения кэша', 'duration'], ['refresh_interval', 'Интервал фонового обновления', 'duration', 'Действует только при включённом кэше.'],
    ['request_timeout', 'Таймаут загрузки', 'duration'], ['max_response_bytes', 'Максимальный размер подписки', 'bytes'],
    ['max_nodes_per_source', 'Максимум узлов из одной подписки', 'number'], ['max_sources', 'Максимум подписок', 'number'],
    ['max_nodes', 'Максимум узлов всего', 'number'],
  ]],
  ['Разнообразие горячего пула', 'pool.provider_diversity', [
    ['enabled', 'Ограничивать узлы одного провайдера', 'boolean'], ['max_per_provider', 'Максимум узлов одного провайдера', 'number'],
  ]],
];
function settingsFieldID(path) { return `setting-${path.replaceAll('.', '-')}`; }
function settingsValue(settings, path) { return path.split('.').reduce((value, key) => value?.[key], settings); }
function cloneSettings(value) { return JSON.parse(JSON.stringify(value)); }
function settingsCurrentDraft() {
  const draft = cloneSettings(settingsSnapshot);
  for (const [, prefix, fields] of settingsSections) {
    const group = settingsValue(draft, prefix);
    for (const [name, , type] of fields) {
      const input = $(`#${settingsFieldID(`${prefix}.${name}`)}`);
      if (type === 'boolean') group[name] = Boolean(input.checked);
      else if (type === 'number' || type === 'decimal') {
        const value = input.value.trim();
        if (!value || !Number.isFinite(Number(value)) || (type === 'number' && !Number.isInteger(Number(value)))) throw new Error('Числовые поля должны содержать корректное число.');
        group[name] = Number(value);
      } else if (type === 'durations') group[name] = input.value.split(',').map((value) => value.trim()).filter(Boolean);
      else group[name] = input.value.trim();
    }
  }
  return draft;
}
const basicSettingPaths = new Set(['benchmark.full_interval', 'benchmark.speed.enabled', 'subscriptions.cache_enabled']);
function settingsFieldHTML(prefix, field) {
  const [name, label, type, hint] = field;
  const path = `${prefix}.${name}`;
  const id = settingsFieldID(path);
  const value = settingsValue(settingsSnapshot, path);
  const input = type === 'boolean'
    ? `<input id="${id}" type="checkbox" ${value ? 'checked' : ''} ${hint ? `aria-describedby="${id}-hint"` : ''}>`
    : `<input id="${id}" type="${['number', 'decimal'].includes(type) ? 'number' : 'text'}" ${type === 'number' ? 'step="1"' : type === 'decimal' ? 'step="any"' : ''} value="${esc(type === 'durations' ? (value || []).join(', ') : value)}" ${type === 'text' ? '' : 'required'} ${hint ? `aria-describedby="${id}-hint"` : ''}>`;
  // Hidden dependencies stay in the form and complete DTO, preserving tuned values.
  const dependency = prefix === 'benchmark.speed' && name !== 'enabled' ? 'speed'
    : prefix === 'subscriptions' && ['cache_ttl', 'refresh_interval'].includes(name) ? 'cache'
    : prefix === 'pool.provider_diversity' && name === 'max_per_provider' ? 'diversity' : '';
  return `<label for="${id}" class="settings-field ${type === 'boolean' ? 'settings-checkbox' : ''}" ${dependency ? `data-settings-dependency="${dependency}"` : ''}><span>${esc(label)}</span>${input}${hint ? `<span id="${id}-hint" class="sub">${esc(hint)}</span>` : ''}${type === 'bytes' ? '<span class="sub">Например: 64 KiB, 1 MiB.</span>' : ''}</label>`;
}
function updateSettingsDependencies() {
  const values = {
    speed: Boolean($(`#${settingsFieldID('benchmark.speed.enabled')}`).checked),
    cache: Boolean($(`#${settingsFieldID('subscriptions.cache_enabled')}`).checked),
    diversity: Boolean($(`#${settingsFieldID('pool.provider_diversity.enabled')}`).checked),
  };
  document.querySelectorAll('[data-settings-dependency]').forEach((field) => {
    field.classList.toggle('hidden', !values[field.dataset.settingsDependency]);
  });
  $('#settings-speed-hint').textContent = values.speed ? 'Настройки измерения доступны ниже.' : 'Измерение скорости выключено. Его параметры сохраняются.';
  $('#settings-cache-hint').textContent = values.cache ? 'Параметры кэша и фонового обновления доступны ниже.' : 'Каждый тест скачивает подписки заново. Фоновое обновление выключено.';
}
function renderSettings(data) {
  settingsSnapshot = cloneSettings(data.settings);
  settingsRevision = data.revision;
  settingsPoolSize = data.pool_size;
  settingsDirty = false;
  settingsNeedsRefresh = false;
  const basic = settingsSections.flatMap(([, prefix, fields]) => fields.filter(([name]) => basicSettingPaths.has(`${prefix}.${name}`)).map((field) => settingsFieldHTML(prefix, field))).join('');
  const advanced = settingsSections.map(([title, prefix, fields]) => {
    const remaining = fields.filter(([name]) => !basicSettingPaths.has(`${prefix}.${name}`));
    return `<details class="settings-advanced"><summary>${esc(title)}</summary><fieldset class="settings-section"><legend class="visually-hidden">${esc(title)}</legend>${prefix === 'benchmark.speed' ? '<p id="settings-speed-hint" class="sub"></p>' : prefix === 'subscriptions' ? '<p id="settings-cache-hint" class="sub"></p>' : ''}<div class="settings-grid">${remaining.map((field) => settingsFieldHTML(prefix, field)).join('')}</div></fieldset></details>`;
  }).join('');
  $('#settings-fields').innerHTML = `<fieldset class="settings-section settings-basic"><legend>Основные</legend><div class="settings-grid">${basic}</div></fieldset><h3 class="settings-advanced-title">Расширенные параметры</h3><p class="sub">Откройте нужную группу. Скрытые параметры сохраняют свои значения.</p>${advanced}<p class="sub">Размер горячего пула: <strong>${esc(data.pool_size)}</strong>. Изменение размера через панель пока недоступно.</p>`;
  // Fill properties as well as markup, so typed values are never reset by background polling.
  for (const [, prefix, fields] of settingsSections) for (const [name, , type] of fields) {
    const input = $(`#${settingsFieldID(`${prefix}.${name}`)}`);
    const value = settingsValue(settingsSnapshot, `${prefix}.${name}`);
    if (type === 'boolean') input.checked = Boolean(value);
    else input.value = String(type === 'durations' ? (value || []).join(', ') : value ?? '');
  }
  $('#settings-error').textContent = '';
  $('#settings-status').textContent = 'Загружены действующие настройки. Время: секунды (s), минуты (m), часы (h).';
  updateSettingsDependencies();
  updateSettingsControls();
}
function updateSettingsControls() {
  const busy = settingsLoading || settingsSaving || settingsChecking || panelTrialActive() || panelMutating;
  const allowed = authenticated && can('config.manage|users.manage');
  $('#settings-form').setAttribute('aria-busy', String(busy));
  $('#settings-save').disabled = !allowed || !settingsSnapshot || !settingsDirty || settingsNeedsRefresh || busy;
  $('#settings-save').textContent = settingsSaving ? 'Применение…' : 'Сохранить';
  $('#settings-validate').disabled = !allowed || !settingsSnapshot || busy;
  $('#settings-discard').disabled = !allowed || !settingsDirty || settingsSaving || settingsLoading || settingsChecking;
  $('#settings-refresh').disabled = !allowed || busy;
  document.querySelectorAll('#settings-fields input').forEach((input) => { input.disabled = !allowed || busy; });
  $('#settings-dirty').textContent = settingsDirty ? 'Есть несохранённые изменения' : 'Нет изменений';
  updatePanelControls();
}
function clearSettingsEditor() {
  settingsGeneration++;
  clearTimeout(settingsApplyTimer);
  settingsApplyTimer = undefined;
  settingsSnapshot = null;
  settingsRevision = '';
  settingsDirty = false;
  settingsLoading = false;
  settingsSaving = false;
  settingsChecking = false;
  settingsTargetRevision = '';
  settingsNeedsRefresh = false;
  settingsApplyDeadline = 0;
  settingsApplyAttempts = 0;
  $('#settings-fields').innerHTML = '';
  $('#settings-error').textContent = '';
  $('#settings-status').textContent = '';
  updateSettingsControls();
}
async function loadSettings(force = false) {
  if (!authenticated || !can('config.manage|users.manage') || settingsLoading || settingsSaving || settingsChecking || (!force && settingsSnapshot)) return;
  if (settingsDirty && !confirm('Отменить несохранённые изменения и загрузить действующие настройки?')) return;
  const generation = ++settingsGeneration;
  settingsLoading = true;
  $('#settings-error').textContent = '';
  $('#settings-status').textContent = 'Загрузка настроек…';
  updateSettingsControls();
  try {
    const data = await api('/api/v1/settings');
    if (generation !== settingsGeneration || !authenticated || !can('config.manage|users.manage')) return;
    renderSettings(data);
  } catch (error) {
    if (generation !== settingsGeneration) return;
    $('#settings-error').textContent = error.message;
    $('#settings-status').textContent = 'Не удалось загрузить настройки. Нажмите «Обновить данные».';
  } finally {
    if (generation === settingsGeneration) { settingsLoading = false; updateSettingsControls(); }
  }
}
function settingsChanged() {
  if (!settingsSnapshot || settingsSaving || settingsChecking) return;
  updateSettingsDependencies();
  try { settingsDirty = JSON.stringify(settingsCurrentDraft()) !== JSON.stringify(settingsSnapshot); }
  catch { settingsDirty = true; }
  $('#settings-error').textContent = '';
  updateSettingsControls();
}
async function validateSettings() {
  if (!authenticated || !can('config.manage|users.manage') || !settingsSnapshot || settingsSaving || settingsChecking || settingsLoading) return;
  const generation = settingsGeneration;
  settingsChecking = true;
  updateSettingsControls();
  try {
    await api('/api/v1/settings/validate', { method: 'POST', body: JSON.stringify({ settings: settingsCurrentDraft(), revision: settingsRevision }) });
    if (generation === settingsGeneration) { $('#settings-error').textContent = ''; $('#settings-status').textContent = 'Параметры корректны. Для применения нажмите «Сохранить».'; }
  } catch (error) { if (generation === settingsGeneration) $('#settings-error').textContent = error.message; }
  finally { if (generation === settingsGeneration) { settingsChecking = false; updateSettingsControls(); } }
}
async function saveSettings(event) {
  event.preventDefault();
  if (!authenticated || !can('config.manage|users.manage') || !settingsSnapshot || !settingsDirty || settingsNeedsRefresh || settingsSaving || settingsChecking || settingsLoading || panelTrialActive() || panelMutating) return;
  const generation = ++settingsGeneration;
  let draft;
  try { draft = settingsCurrentDraft(); } catch (error) { $('#settings-error').textContent = error.message; return; }
  settingsSaving = true;
  settingsTargetRevision = '';
  settingsApplyDeadline = Date.now() + 120000;
  settingsApplyAttempts = 0;
  $('#settings-error').textContent = '';
  $('#settings-status').textContent = 'Сохранение и применение настроек…';
  updateSettingsControls();
  try {
    const data = await api('/api/v1/settings/save', { method: 'POST', body: JSON.stringify({ settings: draft, revision: settingsRevision }) });
    if (generation !== settingsGeneration) return;
    if (!data.changed) {
      settingsSaving = false;
      settingsDirty = false;
      settingsSnapshot = draft;
      settingsRevision = data.revision || settingsRevision;
      settingsTargetRevision = '';
      $('#settings-status').textContent = 'Настройки уже действуют.';
      updateSettingsControls();
      return;
    }
    settingsTargetRevision = data.revision || '';
    $('#settings-status').textContent = 'Настройки сохранены. Ожидаем применения и переподключения…';
    await pollSettingsApply(generation);
  } catch (error) {
    if (generation !== settingsGeneration) return;
    if (!error.status && authenticated && can('config.manage|users.manage')) {
      // The connection can close after accepting a save; never submit it twice automatically.
      $('#settings-status').textContent = 'Связь прервалась. Проверяем результат сохранения…';
      await pollSettingsApply(generation);
      return;
    }
    settingsSaving = false;
    settingsTargetRevision = '';
    settingsNeedsRefresh = error.code === 'settings_conflict';
    $('#settings-error').textContent = error.message;
    $('#settings-status').textContent = 'Настройки не применены. Изменения в форме сохранены.';
    updateSettingsControls();
  }
}
async function pollSettingsApply(generation) {
  if (generation !== settingsGeneration || !settingsSaving || !authenticated || !can('config.manage|users.manage')) return;
  settingsApplyAttempts++;
  try {
    const data = await api('/api/v1/settings', typeof AbortSignal === 'undefined' ? {} : { signal: AbortSignal.timeout(8000) });
    if (generation !== settingsGeneration) return;
    const state = data.apply?.status;
    if (state === 'applied' && settingsTargetRevision && data.apply?.revision === settingsTargetRevision && data.revision === settingsTargetRevision) {
      settingsSaving = false;
      settingsTargetRevision = '';
      renderSettings(data);
      $('#settings-status').textContent = 'Настройки применены. Панель подключена.';
      return;
    }
    if ((state === 'rolled_back' || state === 'failed') && settingsTargetRevision && data.apply?.revision === settingsTargetRevision) {
      settingsSaving = false;
      settingsTargetRevision = '';
      settingsRevision = data.revision;
      $('#settings-error').textContent = state === 'rolled_back' ? 'Не удалось применить параметры. Восстановлены предыдущие настройки; ваши изменения остались в форме.' : 'Не удалось применить параметры. Обновите данные и проверьте действующие настройки.';
      $('#settings-status').textContent = 'Применение завершилось ошибкой.';
      updateSettingsControls();
      return;
    }
    if (!settingsTargetRevision && state !== 'applying') {
      settingsSaving = false;
      settingsNeedsRefresh = true;
      $('#settings-error').textContent = 'Сервер доступен, но ответ на сохранение был потерян. Обновите данные, чтобы проверить действующие настройки.';
      $('#settings-status').textContent = 'Результат сохранения требует проверки. Изменения остались в форме.';
      updateSettingsControls();
      return;
    }
  } catch {
    if (generation !== settingsGeneration) return;
  }
  if (!authenticated || !can('config.manage|users.manage') || generation !== settingsGeneration) return;
  if (settingsApplyAttempts >= 60 || Date.now() >= settingsApplyDeadline) {
    settingsSaving = false;
    settingsTargetRevision = '';
    settingsNeedsRefresh = true;
    $('#settings-error').textContent = 'Панель не подтвердила применение за 2 минуты. Обновите данные перед повторным сохранением.';
    $('#settings-status').textContent = 'Результат применения пока неизвестен.';
    updateSettingsControls();
    return;
  }
  settingsApplyTimer = setTimeout(() => pollSettingsApply(generation), 2000);
}
function initializeSettingsEditor() {
  $('#settings-form').addEventListener('input', settingsChanged);
  $('#settings-form').addEventListener('change', settingsChanged);
  $('#settings-form').addEventListener('submit', saveSettings);
  $('#settings-validate').onclick = validateSettings;
  $('#settings-refresh').onclick = () => loadSettings(true);
  $('#settings-discard').onclick = () => {
    if (panelTrialActive() && settingsSnapshot) { renderSettings({ settings: settingsSnapshot, revision: settingsRevision, pool_size: settingsPoolSize }); updatePanelControls(); }
    else return loadSettings(true);
  };
  if (window.addEventListener) window.addEventListener('beforeunload', (event) => {
    if (!settingsDirty && !settingsSaving && !panelDirty && (!panelTrialActive() || panelNavigationApproved)) return;
    event.preventDefault();
    event.returnValue = '';
  });
}

