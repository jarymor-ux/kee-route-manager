'use strict';

const $ = (selector) => document.querySelector(selector);
let csrf = '';
let statusData = null;
let nodesData = [];
let pollTimer;
let pendingUpdate = null;

async function api(path, options = {}) {
  const method = options.method || 'GET';
  const headers = { Accept: 'application/json', ...(options.headers || {}) };
  if (options.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && method !== 'HEAD' && csrf) headers['X-KRM-CSRF'] = csrf;

  const response = await fetch(path, { ...options, headers, credentials: 'same-origin' });
  const contentType = response.headers.get('content-type') || '';
  const data = contentType.includes('json') ? await response.json() : await response.text();
  if (response.status === 401) {
    showLogin();
    throw new Error('Требуется вход');
  }
  if (!response.ok) throw new Error((data && data.error) || data || `HTTP ${response.status}`);
  return data;
}

function showLogin() {
  clearInterval(pollTimer);
  $('#login').classList.remove('hidden');
  $('#password').value = '';
  setTimeout(() => $('#username').focus(), 50);
}

function hideLogin() {
  $('#login').classList.add('hidden');
  startPolling();
}

function fmtAge(value) {
  if (!value) return '—';
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000));
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
    csrf = data.csrf;
    hideLogin();
  } catch {
    showLogin();
  }
}

async function loadStatus() {
  try {
    const data = await api('/api/v1/status');
    statusData = data;
    renderStatus(data);
  } catch {
    $('#status-dot').className = 'dot bad';
    $('#status-text').textContent = 'Нет связи';
  }
}

function renderStatus(data) {
  const state = data.state;
  const operation = data.operation;
  const running = data.xray_running;
  const capabilities = data.capabilities || {};

  $('#version').textContent = `v${data.version}`;
  $('#status-dot').className = `dot ${running ? (state.direct_mode ? 'warn' : '') : 'bad'}`;
  $('#status-text').textContent = running ? (state.direct_mode ? 'Прямой маршрут' : 'VPN активен') : 'Xray остановлен';
  $('#route-mode').textContent = state.direct_mode ? 'DIRECT' : 'VPN';
  const activeSlot = state.pool?.find((slot) => slot.index === state.active_slot);
  $('#active-node').textContent = state.direct_mode
    ? 'Трафик временно идёт напрямую'
    : activeSlot?.label || state.active_node_id || 'Не выбран';
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

  $('#reboot').classList.toggle('hidden', !capabilities.reboot);
  $('#system-logs').classList.toggle('hidden', !capabilities.system_logs);
  $('#diagnostics').classList.toggle('hidden', !capabilities.diagnostics);

  renderPool(state);
  renderSources(state.sources || {});
  ['benchmark', 'direct', 'restart-xray', 'reboot'].forEach((id) => {
    const element = $(`#${id}`);
    if (element) element.disabled = Boolean(operation && operation.status === 'running');
  });
}

function renderPool(state) {
  $('#pool').innerHTML = (state.pool || []).map((slot) => `
    <article class="pool-item ${slot.index === state.active_slot && !state.direct_mode ? 'active' : ''}">
      <div class="slot">Слот ${slot.index + 1} ${slot.index === state.active_slot && !state.direct_mode ? badge('Активен', 'ok') : ''}</div>
      <div class="name" title="${esc(slot.label || 'Свободен')}">${esc(slot.label || 'Свободен')}</div>
      <div class="stats">${slot.node_id ? `${Math.round(slot.score || 0)} score · ${fmtAge(slot.last_verified_at)}` : 'Нет узла'}</div>
      ${slot.node_id ? `<button class="ghost compact slot-switch" data-index="${slot.index}">Переключить</button>` : ''}
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
}

async function loadRouter() {
  try {
    const [metrics, clients] = await Promise.all([
      api('/api/v1/router/metrics'),
      api('/api/v1/router/clients'),
    ]);
    renderMetrics(metrics);
    $('#clients-freshness').textContent = `Обновлено ${fmtAge(clients.updated_at)}${clients.stale ? ' · данные устарели' : ''}${clients.error ? ` · ${clients.error}` : ''}`;
    renderClients(clients.value || []);
  } catch (error) {
    toast(error.message, true);
  }
}

function renderMetrics(metrics) {
  $('#cpu').textContent = metrics.cpu_percent != null ? `${metrics.cpu_percent.toFixed(0)}%` : '—';
  $('#ram').textContent = metrics.ram_percent != null ? `${metrics.ram_percent.toFixed(0)}%` : '—';
  $('#wan').textContent = metrics.wan_connected === false ? 'Отключён' : metrics.wan_name || '—';
  $('#wan-detail').textContent = [metrics.wan_description, metrics.wan_ip].filter(Boolean).join(' · ') || '—';
  $('#traffic').textContent = `↓ ${metrics.rx_mbps?.toFixed(1) || 0} · ↑ ${metrics.tx_mbps?.toFixed(1) || 0} Мбит/с`;
  $('#temperature').textContent = metrics.temperature_c != null ? `${metrics.temperature_c.toFixed(1)} °C` : '—';
  $('#uptime').textContent = formatUptime(metrics.uptime_seconds || 0);
  const ports = metrics.ports || [];
  $('#ports').innerHTML = ports.length ? ports.map((port) => `
    <div class="port-card"><strong>${esc(port.id || 'Порт')}</strong><div class="sub">${esc(String(port.link ?? '—'))} · ${esc(String(port.speed ?? '—'))}</div></div>`).join('')
    : '<div class="sub">Данные о портах недоступны</div>';
}

function renderClients(clients) {
  const capabilities = statusData?.capabilities || {};
  $('#clients-body').innerHTML = clients.map((client) => `<tr>
    <td><strong>${esc(client.name || client.hostname || 'Без имени')}</strong></td>
    <td>${esc(client.ip || '—')}<br><span class="muted">${esc(client.mac)}</span></td>
    <td>${client.active ? badge('В сети', 'ok') : badge('Не в сети')} ${esc(client.link || client.ssid || '')}</td>
    <td>${esc(client.connection_policy || '—')}</td>
    <td>
      ${capabilities.wake_on_lan ? `<button class="ghost compact wake" data-mac="${esc(client.mac)}">WOL</button>` : ''}
      ${capabilities.client_policy ? `<select class="policy" data-mac="${esc(client.mac)}"><option value="">Политика…</option><option value="xkeen">XKeen</option><option value="default">По умолчанию</option></select>` : ''}
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

async function checkUpdate() {
  try {
    showTool('Проверка подписанного манифеста…');
    pendingUpdate = await api('/api/v1/update/check');
    showTool(pendingUpdate.available
      ? `Доступна версия ${pendingUpdate.latest_version}. Текущая: ${pendingUpdate.current_version}.`
      : `Установлена актуальная версия ${pendingUpdate.current_version}.`);
    if (pendingUpdate.available) showTool(`Доступна версия ${pendingUpdate.latest_version}. Установите проверенный подписанный релиз по инструкции репозитория; автоматическая установка отключена в RC2.`);
  } catch (error) {
    pendingUpdate = null;
    showTool(`Ошибка проверки обновления: ${error.message}`);
  }
}

function startPolling() {
  clearInterval(pollTimer);
  loadStatus();
  pollTimer = setInterval(() => {
    loadStatus();
    const active = document.querySelector('#tabs button.active')?.dataset.tab;
    if (active === 'router') loadRouter();
  }, 3000);
}

$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('#login-error').textContent = '';
  try {
    const data = await api('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: $('#username').value, password: $('#password').value }),
    });
    csrf = data.csrf;
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
  const button = event.target.closest('button[data-tab]');
  if (!button) return;
  document.querySelectorAll('#tabs button').forEach((item) => item.classList.toggle('active', item === button));
  document.querySelectorAll('.tab').forEach((item) => item.classList.toggle('active', item.id === `tab-${button.dataset.tab}`));
  if (button.dataset.tab === 'nodes') loadNodes();
  if (button.dataset.tab === 'events') loadEvents();
  if (button.dataset.tab === 'router') loadRouter();
};

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

if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
session();
