'use strict';

const $ = (selector) => document.querySelector(selector);
let csrf = '';
let permissions = new Set();
let currentUser = null;
let usersData = [];
let editingUserUpdatedAt = '';
let permissionCatalog = [];
let roleTemplates = {};
let activePage = '';
let authGeneration = 0;
let pollLoading = false;
const pagePermissions = { router: 'router.view', devices: 'router.clients', interfaces: 'router.view', system: 'router.system|router.reboot|updates.manage', users: 'users.manage', settings: 'config.manage|users.manage', overview: 'vpn.view', subscriptions: 'subscriptions.view|subscriptions.manage', nodes: 'vpn.view', testing: 'vpn.view', events: 'events.view' };
const pageGroups = { router: 'router', devices: 'router', interfaces: 'router', system: 'router', users: 'router', settings: 'router', overview: 'vpn', subscriptions: 'vpn', nodes: 'vpn', testing: 'vpn', events: 'vpn' };
function can(permission) { return permission.split('|').some((item) => permissions.has(item)); }
function requestPermission(path) {
  if (path.includes('/subscriptions')) return path.endsWith('/subscriptions') ? 'subscriptions.view|subscriptions.manage' : 'subscriptions.manage';
  if (path.includes('/panel/')) return 'config.manage|users.manage';
  if (path.includes('/settings')) return 'config.manage|users.manage';
  if (path.includes('/users')) return 'users.manage';
  if (path.includes('/update/')) return 'updates.manage';
  if (path.includes('/router/clients')) return 'router.clients';
  if (path.includes('/router/logs') || path.includes('/router/diagnostics')) return 'router.system';
  if (path.includes('/router/metrics')) return 'router.view';
  if (path.includes('/nodes')) return 'vpn.view';
  if (path.includes('/events')) return 'events.view';
  if (path.includes('/actions/policy')) return 'router.policy';
  if (path.includes('/actions/wake')) return 'router.wake';
  if (path.includes('/actions/reboot')) return 'router.reboot';
  if (path.includes('/actions/')) return 'vpn.control';
  if (path.endsWith('/status')) return '';
  return '';
}
let statusData = null;
let nodesData = [];
let subscriptionData = [];
let clientsData = [];
let pollTimer;
let pendingUpdate = null;
let updateState = null;
let updateStatusLoading = false;
let updateChecking = false;
let updateSwitching = false;
let updateRequestGeneration = 0;
let updateSubmitting = false;
let authenticated = false;
let documentVersion = '';
let versionReloadRequested = false;

async function api(path, options = {}) {
  const required = requestPermission(path);
  if (required && !can(required)) throw new Error('Недостаточно прав');
  const generation = authGeneration;
  const method = options.method || 'GET';
  const headers = { Accept: 'application/json', ...(options.headers || {}) };
  if (options.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && method !== 'HEAD' && csrf) headers['X-KRM-CSRF'] = csrf;

  const response = await fetch(path, { ...options, headers, credentials: 'same-origin' });
  const contentType = response.headers.get('content-type') || '';
  const data = contentType.includes('json') ? await response.json() : await response.text();
  if (generation !== authGeneration || (required && !can(required))) throw new Error('Доступ изменён');
  if (response.status === 401) {
    showLogin();
    throw new Error('Требуется вход');
  }
  if (!response.ok) throw Object.assign(new Error(errorMessage(data, response.status)), { code: data?.code, status: response.status });
  return data;
}

function errorMessage(data, status) {
  const messages = {
    settings_conflict: 'Настройки уже изменены. Обновите данные, затем повторите изменение.',
    settings_invalid: 'Параметры не прошли проверку. Проверьте значения и повторите.',
    settings_busy: 'Настройки уже применяются или выполняется несовместимая операция. Повторите позже.',
    busy: 'Сейчас выполняется несовместимая операция. Повторите после её завершения.',
    canceled: 'Операция отменена.',
    settings_changed: 'Настройки изменились во время теста. Его результат не применён.',
    conflict: 'Операция конфликтует с текущим состоянием. Обновите данные и повторите.',
    unavailable: 'Операция временно недоступна. Повторите позже.',
    user_changed: 'Пользователь уже изменён. Обновите список и откройте редактор заново.',
  };
  if (data?.code && messages[data.code]) return messages[data.code];
  if (data?.error === 'user changed; reload before saving') return messages.user_changed;
  return data?.error || data || `HTTP ${status}`;
}

function showLogin() {
  authenticated = false;
  authGeneration++;
  updateRequestGeneration++;
  updateChecking = false;
  updateSwitching = false;
  csrf = '';
  permissions = new Set();
  currentUser = null;
  statusData = null;
  nodesData = [];
  clientsData = [];
  usersData = [];
  permissionCatalog = [];
  roleTemplates = {};
  clearUserEditor();
  clearSettingsEditor();
  clearPanelEditor();
  ['pool', 'sources', 'nodes-body', 'subscriptions-body', 'clients-body', 'ports', 'events-list', 'tool-output', 'users-body', 'test-results', 'operations-list', 'usage-chart', 'traffic-chart'].forEach((id) => { $(`#${id}`).innerHTML = ''; $(`#${id}`).textContent = ''; });
  ['cpu', 'ram', 'wan', 'wan-detail', 'traffic', 'temperature', 'uptime', 'interface-summary', 'metrics-freshness', 'clients-freshness', 'pool-count', 'last-test', 'last-test-detail', 'route-mode', 'active-node', 'health', 'health-detail', 'operation', 'operation-detail'].forEach((id) => { $(`#${id}`).textContent = '—'; });
  applyPermissions();
  clearInterval(pollTimer);
  pendingUpdate = null;
  updateState = null;
  subscriptionData = [];
  clearSubscriptionEditor();
  renderUpdate();
  $('#login').classList.remove('hidden');
  $('#password').value = '';
  setTimeout(() => $('#username').focus(), 50);
}

function hideLogin() {
  authenticated = true;
  $('#login').classList.add('hidden');
  startPolling();
}

function fmtAge(value) {
  if (!value) return '—';
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return '—';
  const seconds = Math.max(0, Math.round((Date.now() - timestamp) / 1000));
  if (seconds < 60) return `${seconds} сек назад`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} мин назад`;
  return `${Math.floor(seconds / 3600)} ч назад`;
}

function esc(value) {
  return String(value ?? '').replace(/[&<>'"]/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;',
  })[char]);
}

function badge(text, kind = '') {
  return `<span class="badge ${kind}">${esc(text)}</span>`;
}

function toast(message, bad = false) {
  const element = $('#toast');
  element.textContent = message;
  element.classList.toggle('bad', bad);
  element.classList.remove('hidden');
  setTimeout(() => element.classList.add('hidden'), 4500);
}

function formatUptime(seconds) {
  if (!seconds) return '—';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return [days ? `${days} д` : null, hours ? `${hours} ч` : null, `${minutes} мин`]
    .filter(Boolean).join(' ');
}

async function session() {
  try {
    const data = await api('/api/v1/session');
    acceptSession(data);
    hideLogin();
  } catch {
    showLogin();
  }
}

async function loadStatus() {
  if (!authenticated || versionReloadRequested) return;
  try {
    const data = await api('/api/v1/status');
    if (!authenticated || versionReloadRequested) return;
    const version = typeof data.version === 'string' ? data.version.trim() : '';
    if (version) {
      if (documentVersion && version !== documentVersion) {
        versionReloadRequested = true;
        clearInterval(pollTimer);
        window.location.reload();
        return;
      }
      documentVersion = version;
    }
    statusData = data;
    renderStatus(data);
  } catch {
    const reconnecting = Boolean(updateState?.applying);
    $('#status-dot').className = reconnecting ? 'dot warn' : 'dot bad';
    $('#status-text').textContent = reconnecting ? 'Переподключение после обновления…' : 'Нет связи';
  }
}

function renderStatus(data) {
  const state = data.state || { pool: [], sources: {} };
  const operations = data.operations || (data.operation ? [data.operation] : []);
  const operation = operations.find((item) => ['running', 'cancelling', 'queued'].includes(item.status)) || data.operation;
  renderOperations(operations);
  const running = data.xray_running;
  const capabilities = data.capabilities || {};

  $('#version').textContent = data.version ? `v${data.version}` : '—';
  const activeSlot = state.pool?.find((slot) => slot.index === state.active_slot);
  if (state.automatic_routing_paused) {
    $('#status-dot').className = 'dot warn';
    $('#status-text').textContent = 'Управление приостановлено';
    $('#route-mode').textContent = 'ПАУЗА';
    $('#active-node').textContent = 'Запустите тестирование, чтобы возобновить управление';
  } else if (!state.xray_configured) {
    $('#status-dot').className = 'dot warn';
    $('#status-text').textContent = 'Маршрут не настроен';
    $('#route-mode').textContent = 'НЕ НАСТРОЕН';
    $('#active-node').textContent = 'Запустите тестирование для выбора узла';
  } else {
    $('#status-dot').className = `dot ${running ? (state.direct_mode ? 'warn' : '') : 'bad'}`;
    $('#status-text').textContent = running ? (state.direct_mode ? 'Прямой маршрут' : 'VPN активен') : 'Xray остановлен';
    $('#route-mode').textContent = state.direct_mode ? 'DIRECT' : 'VPN';
    $('#active-node').textContent = state.direct_mode
      ? 'Трафик временно идёт напрямую'
      : activeSlot?.label || state.active_node_id || 'Не выбран';
  }
  $('#health').textContent = state.last_health_at
    ? (state.consecutive_failures ? `Ошибка ×${state.consecutive_failures}` : 'В норме')
    : 'Нет данных';
  $('#health-detail').textContent = `${state.last_health_message || 'Проверка не выполнялась'} · ${fmtAge(state.last_health_at)}`;

  if (operation && operation.status === 'running') {
    $('#operation').textContent = operation.type;
    $('#operation-detail').textContent = operation.message || operation.stage;
    const percent = operation.total ? (100 * operation.current / operation.total) : 8;
    $('#operation-progress').style.width = `${Math.min(100, percent)}%`;
  } else {
    $('#operation').textContent = operation ? operation.status : 'Нет';
    $('#operation-detail').textContent = operation?.error || operation?.message || 'Нет активных операций';
    $('#operation-progress').style.width = operation?.status === 'succeeded' ? '100%' : '0%';
  }

  $('#reboot').classList.toggle('hidden', !capabilities.reboot || !can('router.reboot'));
  $('#system-logs').classList.toggle('hidden', !capabilities.system_logs || !can('router.system'));
  $('#diagnostics').classList.toggle('hidden', !capabilities.diagnostics || !can('router.system'));

  $('#pool-count').textContent = String((state.pool || []).filter((slot) => slot.node_id && slot.healthy && slot.index !== state.active_slot).length);
  $('#last-test').textContent = fmtAge(state.last_benchmark?.finished_at);
  $('#last-test-detail').textContent = state.last_benchmark?.error || (state.last_benchmark?.finished_at ? `Проверено ${state.last_benchmark.tested_count || 0} из ${state.last_benchmark.node_count || 0} узлов` : 'Тест ещё не завершался');
  renderPool(state);
  renderSources(state.sources || {});
  if (!can('vpn.view')) { $('#status-dot').className = 'dot'; $('#status-text').textContent = 'Панель подключена'; }
  ['benchmark', 'test-start'].forEach((id) => { $(`#${id}`).disabled = operations.some((item) => item.type === 'benchmark' && ['running', 'cancelling', 'queued'].includes(item.status)); });
}

function renderPool(state) {
  $('#pool').innerHTML = (state.pool || []).map((slot) => `
    <article class="pool-item ${slot.index === state.active_slot && !state.direct_mode ? 'active' : ''}">
      <div class="slot">Слот ${slot.index + 1} ${slot.index === state.active_slot && !state.direct_mode ? badge('Активен', 'ok') : ''}</div>
      <div class="name" title="${esc(slot.label || 'Свободен')}">${esc(slot.label || 'Свободен')}</div>
      <div class="stats">${slot.node_id ? `${Math.round(slot.score || 0)} score · ${fmtAge(slot.last_verified_at)}` : 'Нет узла'}</div>
      ${slot.node_id && can('vpn.control') ? `<button class="ghost compact slot-switch" data-index="${slot.index}">Переключить</button>` : ''}
    </article>`).join('');
  document.querySelectorAll('.slot-switch').forEach((button) => {
    button.onclick = () => action('/api/v1/actions/switch', { index: Number(button.dataset.index) }, 'Переключение выполнено');
  });
}

function renderSources(sources) {
  const items = Object.values(sources);
  $('#sources').innerHTML = items.length ? items.map((source) => {
    const kind = source.status === 'healthy' ? 'ok' : source.status === 'unavailable' ? 'bad' : 'warn';
    return `<article class="card">
      <div class="eyebrow">${esc(source.name || source.id)}</div>
      <div class="metric small-metric">${source.node_count || 0} узлов</div>
      <div class="source-status">${badge(source.status, kind)}<span class="muted">${source.using_cache ? 'кэш' : 'сеть'}</span></div>
      <div class="sub">${esc(source.last_error || `Обновлено ${fmtAge(source.last_success_at)}`)}</div>
    </article>`;
  }).join('') : '<article class="card"><div class="sub">Источники ещё не загружены</div></article>';
}

function subscriptionEndpointLabel(raw) {
  const value = String(raw || '');
  if (value.startsWith('file:')) return 'Локальный файл';
  const match = value.match(/^([a-z][a-z0-9+.-]*):\/\/([^/?#]+)/i);
  if (!match) return value ? 'URL задан' : '—';
  const authority = match[2].replace(/^[^@]*@/, '');
  return `${match[1]}://${authority}`;
}

function renderSubscriptions() {
  const body = $('#subscriptions-body');
  body.innerHTML = subscriptionData.map((source) => {
    const headerCount = source.header_count ?? Object.keys(source.headers || {}).length;
    return `<tr>
      <td><strong>${esc(source.name || source.id)}</strong></td>
      <td><code>${esc(source.id)}</code></td>
      <td>${esc(source.url ? subscriptionEndpointLabel(source.url) : 'Скрыт')}</td>
      <td>${headerCount ? `${headerCount} шт.` : '—'}</td>
      <td>${source.enabled ? badge('Включена', 'ok') : badge('Выключена')}</td>
      <td>${can('subscriptions.manage') ? `<div class="subscription-actions">
        <button class="ghost compact subscription-edit" data-id="${esc(source.id)}">Изменить</button>
        <button class="danger-outline compact subscription-delete" data-id="${esc(source.id)}">Удалить</button>
      </div>` : 'Только просмотр'}</td>
    </tr>`;
  }).join('') || '<tr><td colspan="6" class="muted">Подписки не настроены</td></tr>';

  document.querySelectorAll('.subscription-edit').forEach((button) => {
    button.onclick = () => openSubscriptionEditor(button.dataset.id);
  });
  document.querySelectorAll('.subscription-delete').forEach((button) => {
    button.onclick = () => deleteSubscription(button.dataset.id);
  });
}

async function loadSubscriptions() {
  try {
    const data = await api('/api/v1/subscriptions');
    subscriptionData = Array.isArray(data.sources) ? data.sources : [];
    renderSubscriptions();
  } catch (error) {
    subscriptionData = [];
    renderSubscriptions();
    toast(error.message, true);
  }
}

function clearSubscriptionEditor() {
  $('#subscription-id').value = '';
  $('#subscription-id').readOnly = false;
  $('#subscription-name').value = '';
  $('#subscription-url').value = '';
  $('#subscription-enabled').checked = true;
  $('#subscription-headers').value = '';
  $('#subscription-error').textContent = '';
  $('#subscription-modal').classList.add('hidden');
}

function openSubscriptionEditor(id = '') {
  if (!can('subscriptions.manage')) return;
  const source = subscriptionData.find((item) => item.id === id);
  $('#subscription-title').textContent = source ? 'Изменить подписку' : 'Добавить подписку';
  $('#subscription-id').value = source?.id || '';
  $('#subscription-id').readOnly = Boolean(source);
  $('#subscription-name').value = source?.name || '';
  $('#subscription-url').value = source?.url || '';
  $('#subscription-enabled').checked = source ? Boolean(source.enabled) : true;
  $('#subscription-headers').value = source?.headers && Object.keys(source.headers).length
    ? JSON.stringify(source.headers, null, 2) : '';
  $('#subscription-error').textContent = '';
  $('#subscription-modal').classList.remove('hidden');
  setTimeout(() => (source ? $('#subscription-name') : $('#subscription-id')).focus(), 0);
}

function parseSubscriptionHeaders() {
  const raw = $('#subscription-headers').value.trim();
  if (!raw) return {};
  const headers = JSON.parse(raw);
  if (!headers || Array.isArray(headers) || typeof headers !== 'object'
      || Object.values(headers).some((value) => typeof value !== 'string')) {
    throw new Error('Headers должны быть JSON-объектом со строковыми значениями');
  }
  return headers;
}

async function saveSubscription(event) {
  event.preventDefault();
  $('#subscription-error').textContent = '';
  try {
    const source = {
      id: $('#subscription-id').value.trim(),
      name: $('#subscription-name').value.trim(),
      url: $('#subscription-url').value.trim(),
      enabled: Boolean($('#subscription-enabled').checked),
      headers: parseSubscriptionHeaders(),
    };
    await api('/api/v1/subscriptions/save', { method: 'POST', body: JSON.stringify(source) });
    clearSubscriptionEditor();
    toast('Подписка сохранена');
    await loadSubscriptions();
    setTimeout(loadStatus, 100);
  } catch (error) {
    $('#subscription-error').textContent = error.message;
  }
}

async function deleteSubscription(id) {
  const source = subscriptionData.find((item) => item.id === id);
  if (!source || !confirm(`Удалить подписку «${source.name || source.id}»?`)) return;
  try {
    await api('/api/v1/subscriptions/delete', { method: 'POST', body: JSON.stringify({ id }) });
    toast('Подписка удалена');
    await loadSubscriptions();
    setTimeout(loadStatus, 100);
  } catch (error) {
    toast(error.message, true);
  }
}

async function action(path, body, success) {
  try {
    await api(path, { method: 'POST', body: JSON.stringify(body || {}) });
    toast(success);
    setTimeout(loadStatus, 400);
  } catch (error) {
    toast(error.message, true);
  }
}

async function loadNodes() {
  try {
    nodesData = await api('/api/v1/nodes');
    renderNodes();
  } catch (error) {
    toast(error.message, true);
  }
}

function renderNodes() {
  const query = $('#node-search').value.toLowerCase();
  const items = nodesData.filter((node) => !query || `${node.label} ${node.sources.join(' ')}`.toLowerCase().includes(query));
  $('#nodes-body').innerHTML = items.map((node) => `<tr>
    <td><strong>${esc(node.label)}</strong>${node.active ? ` ${badge('Активен', 'ok')}` : node.in_pool ? ` ${badge('Пул')}` : ''}</td>
    <td>${esc(node.sources.join(', '))}</td>
    <td>${esc(`${node.network}/${node.security}`)}</td>
    <td>${node.measurement.latency_ms ? `${Math.round(node.measurement.latency_ms)} мс` : '—'}</td>
    <td>${node.measurement.speed_mbps ? `${node.measurement.speed_mbps.toFixed(1)} Мбит/с` : '—'}</td>
    <td>${node.measurement.score ? Math.round(node.measurement.score) : '—'}</td>
    <td>${node.measurement.healthy ? badge('Доступен', 'ok') : node.measurement.checked_at ? badge('Ошибка', 'bad') : badge('Не проверен')}</td>
  </tr>`).join('') || '<tr><td colspan="7" class="muted">Нет узлов</td></tr>';
  $('#test-results').innerHTML = `<table><thead><tr><th>Узел</th><th>Источник</th><th>Тип</th><th>Задержка</th><th>Скорость теста</th><th>Score</th><th>Состояние</th></tr></thead><tbody>${$('#nodes-body').innerHTML}</tbody></table>`;
}

async function loadRouter() {
  try {
    if (activePage === 'devices' && can('router.clients')) {
      const clients = await api('/api/v1/router/clients');
      $('#clients-freshness').textContent = `Обновлено ${fmtAge(clients.updated_at)}${clients.stale ? ' · данные устарели' : ''}${clients.error ? ` · ${clients.error}` : ''}`;
      renderClients(clients.value || []);
    } else if (can('router.view')) {
      const metrics = await api('/api/v1/router/metrics');
      renderMetrics(metrics);
      if (activePage === 'router') {
        const history = await api('/api/v1/router/metrics/history');
        renderCharts((history.samples || []).map((sample) => sample.traffic_available ? sample : { ...sample, rx_mbps: null, tx_mbps: null }));
      }
    }
  } catch (error) { toast(error.message, true); }
}

function renderMetrics(metrics) {
  $('#cpu').textContent = metrics.cpu_percent != null ? `${metrics.cpu_percent.toFixed(0)}%` : '—';
  $('#ram').textContent = metrics.ram_percent != null ? `${metrics.ram_percent.toFixed(0)}%` : '—';
  $('#wan').textContent = metrics.wan_connected === false ? 'Отключён' : metrics.wan_name || '—';
  $('#wan-detail').textContent = [metrics.wan_description, metrics.wan_ip].filter(Boolean).join(' · ') || '—';
  $('#traffic').textContent = `↓ ${metrics.traffic_available && Number.isFinite(metrics.rx_mbps) ? metrics.rx_mbps.toFixed(1) : '—'} · ↑ ${metrics.traffic_available && Number.isFinite(metrics.tx_mbps) ? metrics.tx_mbps.toFixed(1) : '—'} Мбит/с`;
  const stale = metrics.stale || !Number.isFinite(Date.parse(metrics.updated_at)) || Date.now() - Date.parse(metrics.updated_at) > 15000;
  $('#metrics-freshness').textContent = `Обновлено ${fmtAge(metrics.updated_at)}${stale ? ' · данные устарели' : ''}${metrics.error ? ` · ${metrics.error}` : ''}`;
  $('#interface-summary').textContent = [metrics.wan_name, metrics.wan_description, metrics.wan_ip, metrics.wan_connected === false ? 'Отключён' : ''].filter(Boolean).join(' · ') || 'Данные WAN недоступны';
  $('#temperature').textContent = metrics.temperature_c != null ? `${metrics.temperature_c.toFixed(1)} °C` : '—';
  $('#uptime').textContent = formatUptime(metrics.uptime_seconds || 0);
  const ports = metrics.ports || [];
  $('#ports').innerHTML = ports.length ? ports.map((port) => `
    <div class="port-card"><strong>${esc(port.id || 'Порт')}</strong><div class="sub">${esc(String(port.link ?? '—'))} · ${esc(String(port.speed ?? '—'))}</div></div>`).join('')
    : '<div class="sub">Данные о портах недоступны</div>';
}

function renderClients(clients) {
  clientsData = clients;
  const capabilities = statusData?.capabilities || {};
  $('#clients-body').innerHTML = clients.map((client) => `<tr>
    <td><strong>${esc(client.name || client.hostname || 'Без имени')}</strong></td>
    <td>${esc(client.ip || '—')}<br><span class="muted">${esc(client.mac)}</span></td>
    <td>${client.active ? badge('В сети', 'ok') : badge('Не в сети')} ${esc(client.link || client.ssid || '')}</td>
    <td>${esc(client.connection_policy || '—')}</td>
    <td>
      ${capabilities.wake_on_lan && can('router.wake') ? `<button class="ghost compact wake" data-mac="${esc(client.mac)}">WOL</button>` : ''}
      ${capabilities.client_policy && can('router.policy') ? `<select class="policy" data-mac="${esc(client.mac)}"><option value="">Политика…</option><option value="xkeen">XKeen</option><option value="default">По умолчанию</option></select>` : ''}
    </td>
  </tr>`).join('') || '<tr><td colspan="5" class="muted">Клиенты недоступны</td></tr>';
  document.querySelectorAll('.wake').forEach((button) => {
    button.onclick = () => action('/api/v1/actions/wake', { mac: button.dataset.mac }, 'Wake-on-LAN отправлен');
  });
  document.querySelectorAll('.policy').forEach((select) => {
    select.onchange = () => select.value && action('/api/v1/actions/policy', { mac: select.dataset.mac, policy: select.value }, 'Политика изменена');
  });
}

async function loadEvents() {
  try {
    let after = 0;
    let items = [];
    for (let page = 0; page < 10; page++) {
      const batch = await api(`/api/v1/events?after=${after}&limit=1000`);
      for (const event of batch) {
        if (!Number.isSafeInteger(event.sequence) || event.sequence <= after) {
          throw new Error('Некорректная последовательность событий');
        }
        after = event.sequence;
      }
      items = items.concat(batch).slice(-300);
      if (batch.length < 1000) break;
      if (page === 9) throw new Error('Журнал событий меняется слишком быстро; обновите его ещё раз');
    }
    $('#events-list').innerHTML = items.slice().reverse().map((event) => `<div class="event">
      <div class="time">${new Date(event.timestamp).toLocaleString()}</div>
      <div class="type">${esc(event.type)}</div>
      <div><strong>${esc(event.message)}</strong>${event.operation_id ? `<div class="muted">${esc(event.operation_id)}</div>` : ''}</div>
    </div>`).join('') || '<div class="card muted">Событий пока нет</div>';
  } catch (error) {
    toast(error.message, true);
  }
}

function showTool(text) {
  const output = $('#tool-output');
  output.textContent = String(text || '—');
  output.classList.remove('hidden');
  output.scrollTop = 0;
}

async function loadSystemLogs() {
  try {
    showTool('Загрузка…');
    const result = await api('/api/v1/router/logs?lines=200');
    showTool(result.output);
  } catch (error) {
    showTool(`Ошибка: ${error.message}`);
  }
}

async function runDiagnostics() {
  try {
    showTool('Выполняется диагностика…');
    const result = await api('/api/v1/router/diagnostics');
    showTool(result.output);
  } catch (error) {
    showTool(`Ошибка: ${error.message}`);
  }
}

function checkMatchesChannel(check, state = updateState) {
  if (!check) return false;
  // Older launchers have no channel metadata and do not offer switching.
  if (!state?.channel_switch_supported && !state?.channel) return true;
  return (check.channel || check.manifest?.channel) === state?.channel;
}

async function checkUpdate() {
  if (!authenticated || !can('updates.manage') || updateChecking || updateSwitching || updateSubmitting || updateState?.applying) return;
  const generation = updateRequestGeneration;
  updateChecking = true;
  renderUpdate();
  try {
    showTool('Проверка подписанного манифеста…');
    const check = await api('/api/v1/update/check');
    if (generation !== updateRequestGeneration) return;
    pendingUpdate = checkMatchesChannel(check) ? check : null;
    if (!pendingUpdate) { showTool('Канал обновлений изменился. Повторите проверку.'); return; }
    if (pendingUpdate.stage_supported) updateState = { ...updateState, launcher: true, enabled: true, last_error: '' };
    showTool(pendingUpdate.available
      ? `Доступна версия ${pendingUpdate.latest_version}. Текущая: ${pendingUpdate.current_version}.`
      : noNewerUpdateMessage(pendingUpdate));
    if (pendingUpdate.available && !pendingUpdate.stage_supported) {
      showTool(`Доступна версия ${pendingUpdate.latest_version}. Для установки нужен совместимый launcher и подписанный пакет обновления.`);
    }
  } catch (error) {
    if (generation !== updateRequestGeneration) return;
    pendingUpdate = null;
    showTool(`Ошибка проверки обновления: ${error.message}`);
  } finally {
    if (generation === updateRequestGeneration) { updateChecking = false; renderUpdate(); }
  }
}

async function switchUpdateChannel(channel) {
  if (!authenticated || !can('updates.manage') || !updateState?.channel_switch_supported || updateSwitching || updateSubmitting || updateState?.applying || !['rc', 'stable'].includes(channel) || channel === updateState.channel) { renderUpdate(); return; }
  const generation = ++updateRequestGeneration;
  updateSwitching = true;
  updateChecking = false;
  pendingUpdate = null;
  showTool('Сохранение канала обновлений…');
  renderUpdate();
  try {
    const state = await api('/api/v1/update/channel', { method: 'POST', body: JSON.stringify({ channel }) });
    if (generation !== updateRequestGeneration) return;
    updateState = state;
    pendingUpdate = checkMatchesChannel(state.check, state) ? state.check : null;
    showTool(`Выбран канал ${state.channel === 'stable' ? 'Release' : 'Release Candidate (RC)'}. Установленная версия не изменена.`);
    toast('Канал обновлений сохранён');
  } catch (error) {
    if (generation === updateRequestGeneration) { showTool(`Ошибка смены канала: ${error.message}`); toast(error.message, true); }
  } finally {
    if (generation === updateRequestGeneration) { updateSwitching = false; renderUpdate(); }
  }
}

function noNewerUpdateMessage(check) {
  return `Более новой версии в выбранном канале нет. Установлена ${check.current_version || 'неизвестная версия'}.${check.latest_version ? ` Последняя опубликованная: ${check.latest_version}.` : ''}`;
}

function renderUpdate() {
  const busy = updateSubmitting || updateSwitching || Boolean(updateState?.applying);
  const channelSupported = Boolean(updateState?.channel_switch_supported);
  $('#update-channel').value = ['rc', 'stable'].includes(updateState?.channel) ? updateState.channel : '';
  $('#update-channel').disabled = !authenticated || !can('updates.manage') || !channelSupported || busy;
  const unavailableReasons = { disabled: 'Обновления отключены', manual_urls: 'Канал задан URL манифеста; переключение доступно при поиске релизов GitHub' };
  $('#update-channel-hint').textContent = updateSwitching ? 'Сохранение канала…' : !channelSupported ? unavailableReasons[updateState?.channel_switch_reason] || 'Для смены канала требуется обновить стабильный launcher' : `Выбран канал ${updateState.channel === 'stable' ? 'Release' : 'Release Candidate (RC)'}`;
  const installable = authenticated && can('updates.manage') && checkMatchesChannel(pendingUpdate) && pendingUpdate?.available && pendingUpdate?.stage_supported;
  $('#update-apply').classList.toggle('hidden', !installable);
  $('#update-apply').disabled = !installable || busy || updateChecking || updateState?.launcher === false || updateState?.enabled === false;
  $('#update-check').disabled = busy || updateChecking;
  const phases = { downloading: 'Загрузка обновления', preparing: 'Подготовка обновления', trial: 'Проверка новой версии', activating: 'Запуск новой версии' };
  const results = { updated: 'Обновление установлено', installed: 'Установка завершена', rolled_back: 'Восстановлена предыдущая версия' };
  let text = updateSwitching ? 'Сохранение канала обновлений…' : updateChecking
    ? 'Проверка обновлений…'
    : updateState?.reconnecting && updateState?.applying
      ? 'Проверка новой версии… Панель временно переподключается.'
      : phases[updateState?.phase];
  if (!text && updateState?.last_error) text = `Обновление не выполнено: ${updateState.last_error}`;
  if (!text && updateState?.enabled === false) text = 'Обновления отключены';
  if (!text && pendingUpdate) text = pendingUpdate.available
    ? `Доступна версия ${pendingUpdate.latest_version}${pendingUpdate.stage_supported ? ' · установка вручную' : ' · установка через launcher недоступна'}`
    : noNewerUpdateMessage(pendingUpdate);
  if (!text && updateState?.last_result) text = results[updateState.last_result] || updateState.last_result;
  if (!text && updateState?.current_version) text = `Установлена версия ${updateState.current_version}`;
  $('#update-status').textContent = text || 'Статус обновлений пока неизвестен';
}

async function loadUpdateStatus() {
  if (!authenticated || !can('updates.manage') || updateStatusLoading || updateSwitching) return;
  updateStatusLoading = true;
  const generation = updateRequestGeneration;
  try {
    const state = await api('/api/v1/update/status');
    if (!authenticated || generation !== updateRequestGeneration) return;
    if (updateState?.channel && state.channel && state.channel !== updateState.channel) { updateRequestGeneration++; updateChecking = false; }
    updateState = state;
    if (!updateChecking) pendingUpdate = checkMatchesChannel(state.check, state) ? state.check : null;
    renderUpdate();
  } catch (error) {
    if (generation !== updateRequestGeneration) return;
    if (updateState?.applying) {
      updateState = { ...updateState, reconnecting: true, last_error: '' };
    } else {
      updateState = { launcher: false, last_error: error.message };
    }
    renderUpdate();
  } finally {
    updateStatusLoading = false;
  }
}

async function applyUpdate() {
  if (!authenticated || !can('updates.manage') || !checkMatchesChannel(pendingUpdate) || !pendingUpdate?.available || !pendingUpdate?.stage_supported || updateChecking || updateSwitching || updateSubmitting || updateState?.applying || updateState?.launcher === false || updateState?.enabled === false) return;
  const version = pendingUpdate.latest_version;
  const channel = updateState?.channel_switch_supported ? updateState.channel : undefined;
  if (!confirm(`Установить Kee Route Manager ${version}? Панель управления кратковременно переподключится.`)) return;
  updateSubmitting = true;
  renderUpdate();
  try {
    await api('/api/v1/update/apply', { method: 'POST', body: JSON.stringify({ version, channel }) });
    updateState = { ...updateState, launcher: true, enabled: true, applying: true, reconnecting: false, phase: 'downloading', last_error: '' };
    toast('Установка обновления запущена');
  } catch (error) {
    toast(error.message, true);
  } finally {
    updateSubmitting = false;
    renderUpdate();
  }
}

function startPolling() {
  clearInterval(pollTimer);
  loadStatus();
  if (can('updates.manage')) loadUpdateStatus();
  navigate(window.location.hash?.slice(1) || activePage, false);
  pollTimer = setInterval(poll, 3000);
}
async function poll() {
  if (!authenticated || pollLoading || versionReloadRequested) return;
  pollLoading = true;
  try {
    const data = await api('/api/v1/session');
    if (!authenticated) return;
    acceptSession(data);
    await loadStatus();
    if (can('updates.manage')) await loadUpdateStatus();
    await loadPage(activePage);
  } catch (error) { if (authenticated) toast(error.message, true); }
  finally { pollLoading = false; }
}

$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('#login-error').textContent = '';
  try {
    const data = await api('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: $('#username').value, password: $('#password').value }),
    });
    acceptSession(data);
    hideLogin();
  } catch (error) {
    $('#login-error').textContent = error.message;
  }
});

$('#logout').onclick = async () => {
  try { await api('/api/v1/auth/logout', { method: 'POST' }); } catch {}
  csrf = '';
  showLogin();
};

$('#tabs').onclick = (event) => {
  const button = event.target.closest('button');
  if (!button) return;
  if (button.dataset.tab) navigate(button.dataset.tab);
  else if (button.dataset.group) toggleGroup(button.dataset.group);
};
if (window.addEventListener) window.addEventListener('hashchange', () => navigate(window.location.hash.slice(1), false));

$('#add-subscription').onclick = () => openSubscriptionEditor();
$('#subscription-cancel').onclick = clearSubscriptionEditor;
$('#subscription-form').addEventListener('submit', saveSubscription);
$('#test-start').onclick = () => action('/api/v1/actions/benchmark', {}, 'Тестирование запущено');
$('#benchmark-cancel').onclick = () => action('/api/v1/actions/benchmark/cancel', {}, 'Отмена теста запрошена');
$('#benchmark').onclick = () => action('/api/v1/actions/benchmark', {}, 'Тестирование запущено');
$('#direct').onclick = () => confirm('Перевести управляемый трафик напрямую, минуя VPN?')
  && action('/api/v1/actions/direct', {}, 'Включён прямой маршрут');
$('#restart-xray').onclick = () => confirm('Перезапустить Xray? Кратковременный разрыв соединений возможен.')
  && action('/api/v1/actions/xray-restart', {}, 'Xray перезапускается');
$('#reboot').onclick = () => confirm('Перезагрузить устройство?')
  && action('/api/v1/actions/reboot', {}, 'Перезагрузка запущена');
$('#node-search').oninput = renderNodes;
$('#refresh-events').onclick = loadEvents;
$('#system-logs').onclick = loadSystemLogs;
$('#diagnostics').onclick = runDiagnostics;
$('#update-check').onclick = checkUpdate;
$('#update-apply').onclick = applyUpdate;
$('#update-channel').onchange = () => switchUpdateChannel($('#update-channel').value);

$('#add-user').onclick = () => openUserEditor();
$('#user-cancel').onclick = clearUserEditor;
$('#user-form').addEventListener('submit', saveUser);
$('#user-role').onchange = () => {
  const template = roleTemplates[$('#user-role').value];
  if (template) document.querySelectorAll('#user-permissions input').forEach((input) => { input.checked = template.includes(input.value); });
};

function acceptSession(data) {
  csrf = data.csrf || '';
  currentUser = data.user || { username: data.username };
  const next = new Set(Array.isArray(data.permissions) ? data.permissions : []);
  const changed = next.size !== permissions.size || [...next].some((permission) => !permissions.has(permission));
  permissions = next;
  if (changed) {
    // A response started before revocation must not repopulate restricted data.
    authGeneration++;
    if (!can('subscriptions.manage')) clearSubscriptionEditor();
    if (!can('subscriptions.view|subscriptions.manage')) { subscriptionData = []; $('#subscriptions-body').innerHTML = ''; }
    else if (!can('subscriptions.manage')) {
      subscriptionData = subscriptionData.map(({ url, headers, ...source }) => ({ ...source, header_count: source.header_count ?? Object.keys(headers || {}).length }));
      renderSubscriptions();
    }
    if (!can('config.manage|users.manage')) { clearSettingsEditor(); clearPanelEditor(); }
    if (!can('users.manage')) { usersData = []; roleTemplates = {}; permissionCatalog = []; $('#users-body').innerHTML = ''; clearUserEditor(); }
    if (!can('vpn.view')) { statusData = null; nodesData = []; ['route-mode', 'active-node', 'health', 'health-detail', 'operation', 'operation-detail', 'pool-count', 'last-test', 'last-test-detail'].forEach((id) => { $(`#${id}`).textContent = '—'; }); ['pool', 'sources', 'nodes-body', 'operations-list', 'test-results'].forEach((id) => { $(`#${id}`).innerHTML = ''; }); }
    if (!can('router.clients')) { clientsData = []; $('#clients-body').innerHTML = ''; $('#clients-freshness').textContent = '—'; }
    if (!can('router.view')) { ['ports', 'usage-chart', 'traffic-chart'].forEach((id) => { $(`#${id}`).innerHTML = ''; }); ['cpu', 'ram', 'wan', 'wan-detail', 'traffic', 'temperature', 'uptime', 'metrics-freshness', 'interface-summary'].forEach((id) => { $(`#${id}`).textContent = '—'; }); }
    if (!can('events.view')) $('#events-list').innerHTML = '';
    if (can('router.clients')) renderClients(clientsData);
    if (!can('vpn.control')) { renderPool(statusData?.state || {}); }
    if (!can('router.system')) { $('#tool-output').textContent = ''; $('#tool-output').classList.add('hidden'); }
    if (!can('updates.manage')) { updateRequestGeneration++; updateChecking = false; updateSwitching = false; pendingUpdate = null; updateState = null; renderUpdate(); }
    applyPermissions();
    if (statusData) renderStatus(statusData);
    if (!can(pagePermissions[activePage] || '')) navigate(activePage, false);
  }
}

function applyPermissions() {
  document.querySelectorAll('[data-permission]').forEach((element) => {
    element.classList.toggle('hidden', !can(element.dataset.permission));
  });
  ['router', 'vpn'].forEach((group) => {
    const available = Object.keys(pagePermissions).some((page) => pageGroups[page] === group && can(pagePermissions[page]));
    $(`#tabs .nav-group[data-group="${group}"]`).classList.toggle('hidden', !available);
  });
  renderUpdate();
}

function toggleGroup(group, force) {
  const button = $(`#tabs .group-toggle[data-group="${group}"]`);
  const menu = $(`#menu-${group}`);
  const expanded = force ?? menu.classList.contains('hidden');
  menu.classList.toggle('hidden', !expanded);
  button.setAttribute('aria-expanded', String(expanded));
}

function navigate(page, updateHash = true) {
  if (!authenticated) return;
  if (!pagePermissions[page] || !can(pagePermissions[page])) {
    page = Object.keys(pagePermissions).find((item) => can(pagePermissions[item])) || '';
  }
  if (activePage === 'settings' && page !== 'settings' && (settingsDirty || panelDirty) && can('config.manage|users.manage')) {
    if (!confirm('Есть несохранённые настройки. Отменить изменения и перейти?')) {
      if (window.location.hash !== '#settings') window.location.hash = '#settings';
      return;
    }
    clearSettingsEditor();
    if (!panelTrialActive()) clearPanelEditor();
  }
  activePage = page;
  document.querySelectorAll('#tabs [data-tab]').forEach((button) => {
    const active = button.dataset.tab === page;
    button.classList.toggle('active', active);
    button.setAttribute('aria-current', active ? 'page' : 'false');
  });
  document.querySelectorAll('.tab').forEach((section) => section.classList.toggle('active', section.id === `tab-${page}`));
  $('#no-access').classList.toggle('hidden', Boolean(page));
  if (!page) return;
  ['router', 'vpn'].forEach((group) => toggleGroup(group, pageGroups[page] === group));
  if (updateHash && window.location.hash !== `#${page}`) window.location.hash = page;
  loadPage(page);
}

async function loadPage(page) {
  if (!authenticated || !pagePermissions[page] || !can(pagePermissions[page])) return;
  if (page === 'subscriptions') return loadSubscriptions();
  if (page === 'nodes' || page === 'testing') return loadNodes();
  if (page === 'events') return loadEvents();
  if (['router', 'devices', 'interfaces'].includes(page)) return loadRouter();
  if (page === 'users') return loadUsers();
  if (page === 'settings') {
    if (!settingsSnapshot && !settingsLoading && !settingsSaving) await loadSettings();
    return loadPanelStatus();
  }
}

function renderOperations(operations) {
  const labels = { benchmark: 'Тестирование', 'switch-slot': 'Переключение маршрута', 'switch-direct': 'Прямой маршрут', 'client-policy': 'Политика устройства', 'wake-on-lan': 'Wake-on-LAN', 'xray-restart': 'Перезапуск Xray', 'router-reboot': 'Перезагрузка' };
  const statuses = { running: 'Выполняется', queued: 'Ожидает применения', cancelling: 'Отменяется', cancelled: 'Отменена', canceled: 'Отменена', unknown: 'Результат неизвестен', succeeded: 'Завершена', failed: 'Ошибка' };
  $('#operations-list').innerHTML = operations.map((item) => `<article class="card"><strong>${esc(labels[item.type] || item.type)}</strong> ${badge(statuses[item.status] || item.status, item.status === 'failed' ? 'bad' : '')}<p class="sub">${esc(item.error || item.message || item.stage || '')}</p>${item.total ? `<p class="sub">${Number(item.current) || 0} / ${Number(item.total) || 0}</p>` : ''}</article>`).join('') || '<p class="sub">Нет активных операций</p>';
  $('#benchmark-cancel').classList.toggle('hidden', !can('vpn.control') || !operations.some((item) => item.type === 'benchmark' && item.status === 'running'));
}

function chartHTML(samples, series, percent = false) {
  const end = Date.now();
  const begin = end - 3600000;
  const rows = samples.map((sample) => ({ ...sample, time: Date.parse(sample.updated_at) })).filter((sample) => Number.isFinite(sample.time) && sample.time >= begin && sample.time <= end).sort((a, b) => a.time - b.time);
  const values = rows.flatMap((sample) => series.map(([key]) => sample[key])).filter(Number.isFinite);
  if (!values.length) return '<p class="sub">Метрики недоступны</p>';
  const max = percent ? 100 : Math.max(1, ...values);
  const x = (time) => 42 + (time - begin) / 3600000 * 530;
  const y = (value) => 156 - Math.max(0, Math.min(max, value)) / max * 132;
  const lines = series.map(([key, label, color]) => {
    let segments = [], current = [], lastTime = null;
    for (const row of rows) {
      if (!Number.isFinite(row[key]) || (lastTime !== null && row.time - lastTime > 15000)) {
        if (current.length) segments.push(current);
        current = [];
      }
      if (Number.isFinite(row[key])) current.push([x(row.time), y(row[key])]);
      lastTime = row.time;
    }
    if (current.length) segments.push(current);
    return segments.map((segment) => segment.length === 1
      ? `<circle cx="${segment[0][0].toFixed(1)}" cy="${segment[0][1].toFixed(1)}" r="3" fill="${color}"/>`
      : `<polyline points="${segment.map(([px, py]) => `${px.toFixed(1)},${py.toFixed(1)}`).join(' ')}" fill="none" stroke="${color}" stroke-width="2"/>`).join('');
  }).join('');
  return `<svg viewBox="0 0 600 190" role="img" aria-label="${esc(series.map(([, label]) => label).join(' и '))} за последний час"><text x="4" y="28" class="chart-label">${max.toFixed(percent ? 0 : 1)}</text><text x="22" y="156" class="chart-label">0</text><path d="M42 24V156H572" fill="none" stroke="var(--line)"/>${lines}<text x="42" y="182" class="chart-label">−60 мин</text><text x="500" y="182" class="chart-label">Сейчас</text></svg><div class="chart-legend">${series.map(([, label, color]) => `<span><i style="background:${color}"></i>${esc(label)}</span>`).join('')}</div>`;
}

function renderCharts(samples) {
  $('#usage-chart').innerHTML = chartHTML(samples, [['cpu_percent', 'CPU', '#7ea2ff'], ['ram_percent', 'RAM', '#4bd39a']], true);
  $('#traffic-chart').innerHTML = chartHTML(samples, [['rx_mbps', 'Входящий', '#7ea2ff'], ['tx_mbps', 'Исходящий', '#f3bd61']]);
}

const permissionLabels = {
  'vpn.view': 'Просмотр VPN', 'vpn.control': 'Управление VPN и тестированием',
  'subscriptions.view': 'Просмотр подписок', 'subscriptions.manage': 'Изменение подписок и доступ к секретам',
  'router.view': 'Метрики и интерфейсы', 'router.clients': 'Просмотр устройств', 'router.policy': 'Изменение политик',
  'router.wake': 'Wake-on-LAN', 'router.system': 'Системный журнал и диагностика', 'router.reboot': 'Перезагрузка роутера',
  'config.manage': 'Редактирование настроек', 'updates.manage': 'Управление обновлениями', 'users.manage': 'Управление пользователями и правами', 'events.view': 'Просмотр событий',
};

async function loadUsers() {
  try {
    const data = await api('/api/v1/users');
    usersData = Array.isArray(data.users) ? data.users : [];
    permissionCatalog = Array.isArray(data.permissions) ? data.permissions : [];
    roleTemplates = data.roles || {};
    renderUsers();
  } catch (error) { toast(error.message, true); }
}

function renderUsers() {
  $('#users-body').innerHTML = usersData.map((user) => `<tr><td><strong>${esc(user.username)}</strong>${user.id === currentUser?.id ? ' (вы)' : ''}</td><td>${user.enabled ? badge('Включён', 'ok') : badge('Заблокирован', 'bad')}</td><td>${esc((user.permissions || []).map((permission) => permissionLabels[permission] || permission).join(', ') || 'Нет прав')}</td><td><div class="subscription-actions"><button class="ghost compact user-edit" data-id="${esc(user.id)}">Права и пароль</button><button class="ghost compact user-toggle" data-id="${esc(user.id)}">${user.enabled ? 'Заблокировать' : 'Включить'}</button><button class="danger-outline compact user-delete" data-id="${esc(user.id)}">Удалить</button></div></td></tr>`).join('') || '<tr><td colspan="4">Нет пользователей</td></tr>';
  document.querySelectorAll('.user-edit').forEach((button) => { button.onclick = () => openUserEditor(button.dataset.id); });
  document.querySelectorAll('.user-toggle').forEach((button) => { button.onclick = () => toggleUser(button.dataset.id); });
  document.querySelectorAll('.user-delete').forEach((button) => { button.onclick = () => deleteUser(button.dataset.id); });
}

function clearUserEditor() {
  editingUserUpdatedAt = '';
  ['user-id', 'user-name', 'user-password', 'user-role'].forEach((id) => { $(`#${id}`).value = ''; });
  $('#user-permissions').innerHTML = '';
  $('#user-error').textContent = '';
  $('#user-modal').classList.add('hidden');
}

function openUserEditor(id = '') {
  if (!can('users.manage')) return;
  const user = usersData.find((item) => item.id === id);
  clearUserEditor();
  $('#user-title').textContent = user ? 'Изменить пользователя' : 'Добавить пользователя';
  $('#user-id').value = user?.id || '';
  editingUserUpdatedAt = user?.updated_at || '';
  $('#user-name').value = user?.username || '';
  $('#user-password').required = !user;
  $('#user-password-hint').textContent = user ? 'Оставьте пустым, чтобы сохранить пароль. Новый пароль завершит текущие сессии.' : 'Не менее 10 символов';
  $('#user-enabled').checked = user ? Boolean(user.enabled) : true;
  $('#user-permissions').innerHTML = permissionCatalog.map((permission) => `<label class="checkbox-label"><input type="checkbox" value="${esc(permission)}" ${user?.permissions?.includes(permission) ? 'checked' : ''}>${esc(permissionLabels[permission] || permission)}</label>`).join('');
  $('#user-modal').classList.remove('hidden');
  $('#user-name').focus();
}

async function refreshAccess() {
  const data = await api('/api/v1/session');
  acceptSession(data);
}

async function saveUser(event) {
  event.preventDefault();
  try {
    const user = { username: $('#user-name').value.trim(), enabled: Boolean($('#user-enabled').checked), permissions: [...document.querySelectorAll('#user-permissions input')].filter((input) => input.checked).map((input) => input.value) };
    if ($('#user-id').value) { user.id = $('#user-id').value; user.expected_updated_at = editingUserUpdatedAt; }
    if ($('#user-password').value) user.password = $('#user-password').value;
    await api('/api/v1/users/save', { method: 'POST', body: JSON.stringify(user) });
    clearUserEditor();
    toast('Пользователь сохранён');
    await refreshAccess();
    if (can('users.manage')) await loadUsers();
  } catch (error) { $('#user-error').textContent = error.message; }
}

async function toggleUser(id) {
  const user = usersData.find((item) => item.id === id);
  if (!user || !confirm(`${user.enabled ? 'Заблокировать' : 'Включить'} пользователя «${user.username}»?`)) return;
  try {
    await api('/api/v1/users/save', { method: 'POST', body: JSON.stringify({ id: user.id, username: user.username, enabled: !user.enabled, permissions: user.permissions, expected_updated_at: user.updated_at }) });
    await refreshAccess();
    if (can('users.manage')) await loadUsers();
  } catch (error) { toast(error.message, true); }
}

async function deleteUser(id) {
  const user = usersData.find((item) => item.id === id);
  if (!user || !confirm(`Удалить пользователя «${user.username}»?`)) return;
  try {
    await api('/api/v1/users/delete', { method: 'POST', body: JSON.stringify({ id, expected_updated_at: user.updated_at }) });
    await refreshAccess();
    if (can('users.manage')) await loadUsers();
  } catch (error) { toast(error.message, true); }
}

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
function renderSettings(data) {
  settingsSnapshot = cloneSettings(data.settings);
  settingsRevision = data.revision;
  settingsPoolSize = data.pool_size;
  settingsDirty = false;
  settingsNeedsRefresh = false;
  $('#settings-fields').innerHTML = settingsSections.map(([title, prefix, fields]) => `<fieldset class="settings-section"><legend>${esc(title)}</legend><div class="settings-grid">${fields.map(([name, label, type, hint]) => {
    const id = settingsFieldID(`${prefix}.${name}`);
    const value = settingsValue(settingsSnapshot, `${prefix}.${name}`);
    const input = type === 'boolean'
      ? `<input id="${id}" type="checkbox" ${value ? 'checked' : ''}>`
      : `<input id="${id}" type="${['number', 'decimal'].includes(type) ? 'number' : 'text'}" ${type === 'number' ? 'step="1"' : type === 'decimal' ? 'step="any"' : ''} value="${esc(type === 'durations' ? (value || []).join(', ') : value)}" ${type === 'text' ? '' : 'required'} ${hint ? `aria-describedby="${id}-hint"` : ''}>`;
    return `<label for="${id}" class="settings-field ${type === 'boolean' ? 'settings-checkbox' : ''}"><span>${esc(label)}</span>${input}${hint ? `<span id="${id}-hint" class="sub">${esc(hint)}</span>` : ''}${type === 'bytes' ? '<span class="sub">Например: 64 KiB, 1 MiB.</span>' : ''}</label>`;
  }).join('')}</div></fieldset>`).join('') + `<p class="sub">Размер горячего пула: <strong>${esc(data.pool_size)}</strong>. Изменение размера через панель пока недоступно.</p>`;
  // Fill properties as well as markup, so typed values are never reset by background polling.
  for (const [, prefix, fields] of settingsSections) for (const [name, , type] of fields) {
    const input = $(`#${settingsFieldID(`${prefix}.${name}`)}`);
    const value = settingsValue(settingsSnapshot, `${prefix}.${name}`);
    if (type === 'boolean') input.checked = Boolean(value);
    else input.value = String(type === 'durations' ? (value || []).join(', ') : value ?? '');
  }
  $('#settings-error').textContent = '';
  $('#settings-status').textContent = 'Загружены действующие настройки. Время: секунды (s), минуты (m), часы (h).';
  updateSettingsControls();
}
function updateSettingsControls() {
  const busy = settingsLoading || settingsSaving || settingsChecking || panelTrialActive() || panelMutating;
  const allowed = authenticated && can('config.manage|users.manage');
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

let panelState = null;
let panelGeneration = 0;
let panelLoading = false;
let panelMutating = false;
let panelDirty = false;
let panelNeedsRefresh = false;
let panelCertificateURL = '';
let panelCertificatePEM = '';
let panelTimer;
let panelAttempts = 0;
let panelTargetRevision = '';
let panelDeadline = 0;
let panelNavigationApproved = false;
const panelStatuses = new Set(['idle', 'prepared', 'applying', 'awaiting_confirmation', 'applied', 'rolled_back', 'failed']);
function panelTrialActive() { return ['applying', 'awaiting_confirmation'].includes(panelState?.status) || Boolean(panelTargetRevision); }
function validPanelHostname(hostname) {
  return typeof hostname === 'string' && hostname.length <= 253 && hostname === hostname.toLowerCase() && !validPanelIP(hostname) && !['localhost', 'local'].includes(hostname) && !hostname.endsWith('.local') && !hostname.endsWith('.localhost')
    && hostname.split('.').every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
}
function validPanelIP(ip) {
  if (typeof ip !== 'string') return false;
  if (/^\d+\.\d+\.\d+\.\d+$/.test(ip)) return ip.split('.').every((part) => Number(part) <= 255);
  if (!/^[a-fA-F0-9:]+$/.test(ip) || !ip.includes(':')) return false;
  try { return new URL(`https://[${ip}]/`).hostname.startsWith('['); } catch { return false; }
}
function samePanelIP(first, second) {
  if (!validPanelIP(first) || !validPanelIP(second)) return false;
  const canonical = (ip) => new URL(`https://${ip.includes(':') ? `[${ip}]` : ip}/`).hostname;
  return canonical(first) === canonical(second);
}
function validatedPanelStatus(data) {
  if (!data || typeof data.supported !== 'boolean' || !panelStatuses.has(data.status)) throw new Error('Панель вернула некорректное состояние адреса.');
  if (!data.supported) return { supported: false, status: data.status, error: typeof data.error === 'string' ? data.error : '' };
  if (!(validPanelHostname(data.hostname) || samePanelIP(data.hostname, data.listen_ip)) || !Number.isInteger(data.port) || data.port < 1 || data.port > 65535
      || !validPanelIP(data.listen_ip) || (data.revision !== undefined && typeof data.revision !== 'string') || (data.status !== 'idle' && !data.revision)
      || typeof data.dns_automatic !== 'boolean' || typeof data.certificate_changed !== 'boolean') throw new Error('Панель вернула некорректные параметры адреса.');
  let url;
  try { url = new URL(data.url); } catch { throw new Error('Некорректная ссылка панели.'); }
  if (url.protocol !== 'https:' || url.hostname.replace(/^\[|\]$/g, '') !== data.hostname || Number(url.port || 443) !== data.port
      || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Ссылка панели не соответствует её имени и порту.');
  if (data.certificate_pem !== undefined && (typeof data.certificate_pem !== 'string' || data.certificate_pem.length > 65536
      || !/^-----BEGIN CERTIFICATE-----\r?\n[A-Za-z0-9+/=\r\n]+-----END CERTIFICATE-----\s*$/.test(data.certificate_pem))) throw new Error('Панель вернула некорректный публичный сертификат.');
  if (data.confirmation_deadline && !Number.isFinite(Date.parse(data.confirmation_deadline))) throw new Error('Некорректный срок подтверждения адреса.');
  return { ...data, revision: data.revision || '', url: url.origin };
}
function panelInput() {
  const hostname = $('#panel-hostname').value.trim().toLowerCase();
  const rawPort = $('#panel-port').value.trim();
  const port = Number(rawPort);
  if (!(validPanelHostname(hostname) || samePanelIP(hostname, panelState?.listen_ip)) || !rawPort || !Number.isInteger(port) || port < 1 || port > 65535 || port === 9443) throw new Error('Укажите локальное имя или текущий IP панели и HTTPS-порт 1–65535. Другой IP недоступен; порт 9443 занят API controller.');
  return { hostname, port };
}
function panelDraftMatches() {
  try { const input = panelInput(); return input.hostname === panelState?.hostname && input.port === panelState?.port; } catch { return false; }
}
function updatePanelControls() {
  const allowed = authenticated && can('config.manage|users.manage') && panelState?.supported;
  const trial = panelTrialActive();
  const busy = panelLoading || panelMutating || settingsSaving || settingsLoading || settingsChecking;
  $('#panel-hostname').disabled = !allowed || busy || trial;
  $('#panel-port').disabled = !allowed || busy || trial;
  $('#panel-prepare').disabled = !allowed || busy || trial || panelNeedsRefresh;
  const prepared = panelState?.status === 'prepared' && panelDraftMatches();
  $('#panel-apply').classList.toggle('hidden', !prepared);
  $('#panel-apply').disabled = !allowed || busy || trial || panelNeedsRefresh || (panelState?.certificate_changed && !panelCertificateURL);
  const confirm = panelState?.status === 'awaiting_confirmation' && (!panelTargetRevision || panelState.revision === panelTargetRevision);
  $('#panel-confirm').classList.toggle('hidden', !confirm);
  $('#panel-confirm').disabled = !allowed || busy || !confirm || settingsDirty || panelNeedsRefresh;
  $('#panel-confirm-hint').textContent = confirm && settingsDirty ? 'Перед подтверждением отмените несохранённые настройки ниже: переход на новый адрес очистит черновик.' : '';
  $('#panel-refresh').disabled = !allowed || panelLoading || panelMutating;
  $('#panel-open').classList.toggle('hidden', !allowed || !['prepared', 'applying', 'awaiting_confirmation', 'applied'].includes(panelState?.status) || !panelDraftMatches());
  $('#panel-certificate').classList.toggle('hidden', !allowed || !panelCertificateURL || !panelDraftMatches());
}
function clearPanelCertificate() {
  if (panelCertificateURL) URL.revokeObjectURL(panelCertificateURL);
  panelCertificateURL = '';
  panelCertificatePEM = '';
  $('#panel-certificate-download').removeAttribute('href');
  $('#panel-certificate-fingerprint').textContent = '';
  $('#panel-certificate').classList.add('hidden');
}
async function updatePanelCertificate(data, generation) {
  if (!data.certificate_changed || !data.certificate_pem) { clearPanelCertificate(); return; }
  if (panelCertificatePEM === data.certificate_pem) return;
  clearPanelCertificate();
  panelCertificatePEM = data.certificate_pem;
  panelCertificateURL = URL.createObjectURL(new Blob([data.certificate_pem], { type: 'application/x-x509-ca-cert' }));
  $('#panel-certificate-download').href = panelCertificateURL;
  $('#panel-certificate-download').download = `${data.hostname}.crt`;
  if (typeof crypto === 'undefined' || !crypto.subtle || typeof atob === 'undefined') return;
  try {
    const der = Uint8Array.from(atob(data.certificate_pem.replace(/-----[^-]+-----|\s/g, '')), (char) => char.charCodeAt(0));
    const digest = await crypto.subtle.digest('SHA-256', der);
    if (generation !== panelGeneration || panelCertificatePEM !== data.certificate_pem) return;
    $('#panel-certificate-fingerprint').textContent = `SHA-256: ${Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join(':')}`;
  } catch { if (generation === panelGeneration) $('#panel-certificate-fingerprint').textContent = 'Отпечаток сертификата можно проверить после скачивания.'; }
}
function renderPanelStatus(data) {
  panelState = data;
  $('#panel-form').classList.toggle('hidden', !data.supported);
  if (!data.supported) {
    clearPanelCertificate();
    $('#panel-current').textContent = '';
    $('#panel-status').textContent = data.error || 'Редактор адреса недоступен. Требуется обновить стабильный launcher.';
    updatePanelControls();
    return;
  }
  const origin = window.location.origin;
  $('#panel-current').textContent = `Адрес этой панели: ${typeof origin === 'string' && origin.startsWith('https://') ? origin : data.url}. Локальный IP: ${data.listen_ip}.`;
  if (!panelDirty) {
    $('#panel-hostname').value = data.hostname;
    $('#panel-port').value = String(data.port);
  }
  $('#panel-listen-ip').value = data.listen_ip;
  $('#panel-dns').textContent = data.dns_automatic
    ? 'На Keenetic локальная DNS-запись создаётся автоматически при применении. Устройства должны использовать DNS роутера.'
    : `Перед применением настройте в локальном DNS запись ${data.hostname} → ${data.listen_ip}. Публичная регистрация домена не требуется.`;
  $('#panel-preview').textContent = `Подготовленный адрес: ${data.url}`;
  $('#panel-open').href = `${data.url}/#settings`;
  const messages = {
    idle: 'Действующий адрес загружен.', prepared: 'Адрес подготовлен. Проверьте DNS и доверие сертификату, затем примените.',
    applying: 'Применяем адрес. Дождитесь возможности открыть его.', awaiting_confirmation: 'Откройте новый адрес, проверьте вход и подтвердите работу.',
    applied: 'Новый адрес подтверждён.', rolled_back: 'Прежний адрес автоматически восстановлен. Подготовьте изменение заново.', failed: 'Не удалось применить адрес. Действующие настройки сохранены.',
  };
  $('#panel-status').textContent = messages[data.status] + (data.status === 'awaiting_confirmation' && data.confirmation_deadline ? ` Подтверждение до ${new Date(data.confirmation_deadline).toLocaleTimeString()}.` : '');
  if (data.error) $('#panel-error').textContent = String(data.error);
  updatePanelCertificate(data, panelGeneration).then(() => { if (panelState === data) updatePanelControls(); });
  updatePanelControls();
}
function clearPanelEditor() {
  panelGeneration++;
  clearTimeout(panelTimer);
  panelTimer = undefined;
  panelState = null;
  panelLoading = false;
  panelMutating = false;
  panelDirty = false;
  panelNeedsRefresh = false;
  panelTargetRevision = '';
  panelDeadline = 0;
  panelAttempts = 0;
  panelNavigationApproved = false;
  clearPanelCertificate();
  ['panel-hostname', 'panel-port', 'panel-listen-ip'].forEach((id) => { $(`#${id}`).value = ''; });
  ['panel-current', 'panel-status', 'panel-dns', 'panel-preview', 'panel-error', 'panel-confirm-hint'].forEach((id) => { $(`#${id}`).textContent = ''; });
  $('#panel-open').removeAttribute('href');
  $('#panel-form').classList.add('hidden');
  updatePanelControls();
}
async function loadPanelStatus(force = false) {
  if (!authenticated || !can('config.manage|users.manage') || panelLoading || panelMutating || (!force && panelTrialActive())) return;
  const generation = panelGeneration;
  panelLoading = true;
  updatePanelControls();
  try {
    const data = validatedPanelStatus(await api('/api/v1/panel/status'));
    if (generation !== panelGeneration) return;
    if (force) { panelNeedsRefresh = false; $('#panel-error').textContent = ''; }
    renderPanelStatus(data);
    if (data.supported && ['applying', 'awaiting_confirmation'].includes(data.status)) {
      panelTargetRevision = data.revision;
      beginPanelPolling();
    }
  } catch (error) { if (generation === panelGeneration) { $('#panel-error').textContent = error.message; $('#panel-status').textContent = 'Не удалось получить адрес панели. ' + error.message; } }
  finally { if (generation === panelGeneration) { panelLoading = false; updateSettingsControls(); } }
}
async function preparePanel(event) {
  event.preventDefault();
  if (!authenticated || !can('config.manage|users.manage') || !panelState?.supported || panelLoading || panelMutating || panelTrialActive() || panelNeedsRefresh || settingsSaving || settingsLoading || settingsChecking) return;
  let input;
  try { input = panelInput(); } catch (error) { $('#panel-error').textContent = error.message; return; }
  const generation = ++panelGeneration;
  panelMutating = true;
  $('#panel-error').textContent = '';
  updateSettingsControls();
  try {
    const fixedIP = panelState.listen_ip;
    const data = validatedPanelStatus(await api('/api/v1/panel/prepare', { method: 'POST', body: JSON.stringify(input) }));
    if (generation !== panelGeneration) return;
    if (!data.supported || data.status !== 'prepared' || data.hostname !== input.hostname || data.port !== input.port || data.listen_ip !== fixedIP) throw new Error('Подготовленный адрес не соответствует запросу. Обновите состояние.');
    panelDirty = false;
    renderPanelStatus(data);
  } catch (error) { if (generation === panelGeneration) { $('#panel-error').textContent = error.message; panelNeedsRefresh = true; } }
  finally { if (generation === panelGeneration) { panelMutating = false; updateSettingsControls(); } }
}
function beginPanelPolling() {
  clearTimeout(panelTimer);
  panelAttempts = 0;
  const serverDeadline = Date.parse(panelState?.confirmation_deadline || '');
  panelDeadline = Math.min(Date.now() + 315000, Number.isFinite(serverDeadline) && serverDeadline > 0 ? serverDeadline + 15000 : Date.now() + 315000);
  panelTimer = setTimeout(() => pollPanelTrial(panelGeneration), 2000);
}
async function applyPanel() {
  if (!authenticated || !can('config.manage|users.manage') || panelState?.status !== 'prepared' || !panelDraftMatches() || panelLoading || panelMutating || panelTrialActive() || panelNeedsRefresh || (panelState.certificate_changed && !panelCertificateURL) || settingsSaving || settingsChecking || settingsLoading) return;
  const generation = ++panelGeneration;
  const revision = panelState.revision;
  panelTargetRevision = revision;
  panelMutating = true;
  $('#panel-error').textContent = '';
  updateSettingsControls();
  try {
    const result = await api('/api/v1/panel/apply', { method: 'POST', body: JSON.stringify({ revision }) });
    if (generation !== panelGeneration) return;
    if (!result.accepted) throw Object.assign(new Error('Панель не приняла изменение адреса.'), { status: 409 });
    panelDirty = false;
    renderPanelStatus({ ...panelState, status: 'applying' });
    beginPanelPolling();
  } catch (error) {
    if (generation !== panelGeneration) return;
    if (!error.status) {
      $('#panel-status').textContent = 'Связь прервалась. Проверяем состояние адреса без повторного применения…';
      beginPanelPolling();
    } else {
      panelTargetRevision = '';
      panelNeedsRefresh = true;
      $('#panel-error').textContent = error.message;
    }
  } finally { if (generation === panelGeneration) { panelMutating = false; updateSettingsControls(); } }
}
async function pollPanelTrial(generation) {
  if (generation !== panelGeneration || !authenticated || !can('config.manage|users.manage') || !panelTrialActive() || panelMutating) return;
  panelAttempts++;
  try {
    const data = validatedPanelStatus(await api('/api/v1/panel/status', typeof AbortSignal === 'undefined' ? {} : { signal: AbortSignal.timeout(8000) }));
    if (generation !== panelGeneration) return;
    if (data.supported && data.revision === panelTargetRevision) {
      renderPanelStatus(data);
      if (['applied', 'rolled_back', 'failed', 'idle'].includes(data.status)) {
        panelTargetRevision = '';
        clearTimeout(panelTimer);
        updateSettingsControls();
        return;
      }
    } else if (data.supported && ['rolled_back', 'failed', 'idle'].includes(data.status)) {
      // Some launchers report the restored configuration revision after rollback.
      panelTargetRevision = '';
      panelNeedsRefresh = true;
      $('#panel-status').textContent = 'Проверка завершилась. Обновите состояние, чтобы увидеть действующий адрес.';
      updateSettingsControls();
      return;
    }
  } catch { if (generation !== panelGeneration) return; }
  if (generation !== panelGeneration || !authenticated || !can('config.manage|users.manage')) return;
  if (panelAttempts >= 150 || Date.now() >= panelDeadline) {
    panelTargetRevision = '';
    panelNeedsRefresh = true;
    panelState = panelState ? { ...panelState, status: 'failed' } : null;
    $('#panel-error').textContent = 'Не удалось получить итог изменения адреса. Без подтверждения launcher возвращает прежний адрес через 5 минут. Обновите состояние по прежнему адресу.';
    $('#panel-status').textContent = 'Связь с панелью требует проверки.';
    updateSettingsControls();
    return;
  }
  panelTimer = setTimeout(() => pollPanelTrial(generation), 2000);
}
async function confirmPanel() {
  if (!authenticated || !can('config.manage|users.manage') || panelState?.status !== 'awaiting_confirmation' || panelLoading || panelMutating || settingsDirty || settingsSaving || settingsLoading || settingsChecking || panelNeedsRefresh) return;
  const generation = ++panelGeneration;
  const target = validatedPanelStatus(panelState);
  panelMutating = true;
  clearTimeout(panelTimer);
  $('#panel-error').textContent = '';
  updateSettingsControls();
  try {
    const result = await api('/api/v1/panel/confirm', { method: 'POST', body: JSON.stringify({ revision: target.revision }) });
    if (generation !== panelGeneration) return;
    if (!result.accepted) throw Object.assign(new Error('Подтверждение не принято. Обновите состояние.'), { status: 409 });
    panelNavigationApproved = true;
    $('#panel-status').textContent = 'Адрес подтверждён. Открываем его; при смене имени потребуется войти снова.';
    window.location.assign(`${target.url}/#settings`);
  } catch (error) {
    if (generation !== panelGeneration) return;
    panelNeedsRefresh = Boolean(error.status);
    $('#panel-error').textContent = error.status ? error.message : 'Ответ на подтверждение потерян. Проверьте новый адрес; повторное подтверждение автоматически не отправляется.';
    beginPanelPolling();
  } finally { if (generation === panelGeneration) { panelMutating = false; updateSettingsControls(); } }
}
function initializePanelEditor() {
  $('#panel-form').addEventListener('submit', preparePanel);
  const changed = () => {
    if (panelMutating || panelTrialActive()) return;
    panelDirty = !panelDraftMatches();
    $('#panel-error').textContent = '';
    updatePanelControls();
  };
  $('#panel-hostname').addEventListener('input', changed);
  $('#panel-port').addEventListener('input', changed);
  $('#panel-apply').onclick = applyPanel;
  $('#panel-confirm').onclick = confirmPanel;
  $('#panel-refresh').onclick = () => loadPanelStatus(true);
}

// Configuration editor is initialized before the initial session request.
initializeSettingsEditor();
initializePanelEditor();

if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
session();
