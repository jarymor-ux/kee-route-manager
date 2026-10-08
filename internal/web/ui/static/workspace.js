'use strict';

// Leave native selects and focused disclosure controls intact during background refresh.
// Session revocation bypasses this guard so removed rights disappear immediately.
function updateWorkspaceHTML(element, html, force = false) {
  if (element.innerHTML === html) return false;
  if (!force && document.activeElement && element.contains?.(document.activeElement)) return false;
  element.innerHTML = html;
  return true;
}

// Changed action rows return keyboard focus to the same
// keyed control. Selectors are fixed by callers; router/user IDs are compared
// as data values, never interpolated into a CSS selector.
function captureWorkspaceFocus(element, controls, key) {
  const focused = document.activeElement;
  if (!focused || !element.contains?.(focused)) return null;
  return { selector: controls.find((selector) => focused.classList.contains(selector.slice(1))), value: focused.dataset[key] };
}
function restoreWorkspaceFocus(element, focused, key, fallback) {
  if (!focused) return;
  const target = focused.selector
    ? [...(element.querySelectorAll?.(focused.selector) || [])].find((control) => control.dataset[key] === focused.value)
    : null;
  const next = target || $(fallback);
  if (next && !next.disabled && !next.closest?.('.hidden')) next.focus();
}

function nodeState(node) {
  return node.measurement?.healthy ? 'healthy' : node.measurement?.checked_at ? 'error' : 'unchecked';
}

function filteredNodes() {
  const query = $('#node-search').value.toLowerCase();
  const source = $('#node-source-filter')?.value || '';
  const state = $('#node-status-filter')?.value || '';
  const sort = $('#node-sort')?.value || 'label';
  const items = nodesData.filter((node) => (!query || `${node.label} ${(node.sources || []).join(' ')}`.toLowerCase().includes(query))
    && (!source || (node.sources || []).includes(source))
    && (!state || (state === 'active' ? node.active : state === 'pool' ? node.in_pool : nodeState(node) === state)));
  return items.sort((a, b) => {
    if (sort !== 'label') {
      const key = { latency: 'latency_ms', speed: 'speed_mbps', score: 'score' }[sort];
      const av = a.measurement?.[key], bv = b.measurement?.[key];
      const aValid = Number.isFinite(av), bValid = Number.isFinite(bv);
      if (aValid !== bValid) return aValid ? -1 : 1;
      if (aValid && av !== bv) return sort === 'latency' ? av - bv : bv - av;
    }
    return String(a.label || '').localeCompare(String(b.label || ''), 'ru');
  });
}

function nodeRows(items) {
  return items.map((node) => {
    const measurement = node.measurement || {};
    const state = nodeState(node);
    const latency = Number.isFinite(measurement.latency_ms) ? `${Math.round(measurement.latency_ms)} мс` : '—';
    const speed = Number.isFinite(measurement.speed_mbps) ? `${measurement.speed_mbps.toFixed(1)} Мбит/с` : '—';
    const score = Number.isFinite(measurement.score) ? Math.round(measurement.score) : '—';
    const source = esc((node.sources || []).join(', ') || '—');
    const type = esc(`${node.network || '—'}/${node.security || '—'}`);
    return `<tr>
      <td><div class="node-primary"><strong>${esc(node.label)}</strong>${node.active ? ` ${badge('Активен', 'ok')}` : node.in_pool ? ` ${badge('Пул')}` : ''}</div>
      <span class="node-mobile-meta">${source} · ${state === 'healthy' ? badge('Доступен', 'ok') : state === 'error' ? badge('Ошибка', 'bad') : badge('Не проверен')}</span><details class="node-details"><summary>Подробнее<span class="sr-only"> об узле ${esc(node.label)}</span></summary><dl><dt>Источник</dt><dd>${source}</dd><dt>Тип</dt><dd>${type}</dd><dt>Score</dt><dd>${score}</dd></dl></details></td>
      <td>${source}</td><td>${type}</td><td>${latency}</td><td>${speed}</td><td>${score}</td>
      <td>${state === 'healthy' ? badge('Доступен', 'ok') : state === 'error' ? badge('Ошибка', 'bad') : badge('Не проверен')}</td>
    </tr>`;
  }).join('') || '<tr><td colspan="7" class="muted">Нет узлов</td></tr>';
}

function renderNodes() {
  const sourceFilter = $('#node-source-filter');
  if (sourceFilter) {
    const selected = sourceFilter.value;
    const sources = [...new Set(nodesData.flatMap((node) => node.sources || []))].sort();
    const available = sources.includes(selected);
    updateWorkspaceHTML(sourceFilter, `<option value="">Все источники</option>${sources.map((source) => `<option value="${esc(source)}" ${source === selected ? 'selected' : ''}>${esc(source)}</option>`).join('')}`);
    if (!available && selected && document.activeElement !== sourceFilter) sourceFilter.value = '';
  }
  updateWorkspaceHTML($('#nodes-body'), nodeRows(filteredNodes()));
  updateWorkspaceHTML($('#test-results'), `<table class="nodes-table"><thead><tr><th>Узел</th><th>Источник</th><th>Тип</th><th>Задержка</th><th>Скорость теста</th><th>Score</th><th>Состояние</th></tr></thead><tbody>${nodeRows(nodesData)}</tbody></table>`);
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

function renderClientCells(client) {
  return [
    `<strong>${esc(client.name || client.hostname || 'Без имени')}</strong>`,
    `${esc(client.ip || '—')}<br><span class="muted">${esc(client.mac)}</span>`,
    `${client.active ? badge('В сети', 'ok') : badge('Не в сети')} ${esc(client.link || client.ssid || '')}`,
    esc(client.connection_policy || '—'),
  ];
}
function patchClientMetadata(body, clients) {
  const byMAC = new Map(clients.map((client) => [String(client.mac || ''), client]));
  [...(body.querySelectorAll?.('tr[data-client-mac]') || [])].forEach((row) => {
    const client = row.dataset.clientMac ? byMAC.get(row.dataset.clientMac) : null;
    if (!client) return; // Membership changes wait until the native control closes.
    renderClientCells(client).forEach((html, index) => {
      const cell = row.cells[index];
      if (cell && cell.innerHTML !== html) cell.innerHTML = html;
    });
    row.querySelector?.('.policy')?.setAttribute('aria-label', `Политика устройства ${client.name || client.hostname || client.mac}`);
  });
}

function renderClients(clients, force = false) {
  clientsData = clients;
  const capabilities = statusData?.capabilities || {};
  const html = clients.map((client) => `<tr data-client-mac="${esc(client.mac || '')}">
    ${renderClientCells(client).map((cell) => `<td>${cell}</td>`).join('')}
    <td>
      ${capabilities.wake_on_lan && can('router.wake') ? `<button class="ghost compact wake" data-mac="${esc(client.mac)}">WOL</button>` : ''}
      ${capabilities.client_policy && can('router.policy') ? `<select class="policy" aria-label="Политика устройства ${esc(client.name || client.hostname || client.mac)}" data-mac="${esc(client.mac)}"><option value="">Политика…</option><option value="xkeen">XKeen</option><option value="default">По умолчанию</option></select>` : ''}
    </td>
  </tr>`).join('') || '<tr><td colspan="5" class="muted">Клиенты недоступны</td></tr>';
  const body = $('#clients-body');
  if (!updateWorkspaceHTML(body, html, force)) {
    if (!force && body.contains?.(document.activeElement)) patchClientMetadata(body, clients);
    return;
  }
  document.querySelectorAll('.wake').forEach((button) => {
    button.onclick = () => action('/api/v1/actions/wake', { mac: button.dataset.mac }, 'Wake-on-LAN отправлен');
  });
  document.querySelectorAll('.policy').forEach((select) => {
    select.onchange = () => select.value && action('/api/v1/actions/policy', { mac: select.dataset.mac, policy: select.value }, 'Политика изменена');
  });
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
  closeNavigation();
  if ($('#page-title')) $('#page-title').textContent = pageTitles[page] || 'Нет доступных разделов';
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


const pageTitles = { router: 'Дашборд роутера', devices: 'Устройства и политики', interfaces: 'Интерфейсы', system: 'Система', settings: 'Настройки', users: 'Пользователи', overview: 'Обзор VPN', subscriptions: 'Подписки', nodes: 'Узлы', testing: 'Тестирование', events: 'События' };
let navigationReturnFocus = null;
function openNavigation() {
  if (!authenticated) return;
  navigationReturnFocus = document.activeElement;
  document.body?.classList.add('nav-open');
  $('#nav-toggle')?.setAttribute('aria-expanded', 'true');
  $('#nav-backdrop')?.classList.remove('hidden');
  syncOverlayInert();
  $('#nav-close')?.focus();
}
function closeNavigation() {
  const opened = document.body?.classList.contains('nav-open');
  document.body?.classList.remove('nav-open');
  $('#nav-toggle')?.setAttribute('aria-expanded', 'false');
  $('#nav-backdrop')?.classList.add('hidden');
  syncOverlayInert();
  if (opened && navigationReturnFocus?.isConnected && !navigationReturnFocus.closest?.('.hidden')) navigationReturnFocus.focus();
  navigationReturnFocus = null;
}
function initializeWorkspace() {
  $('#node-search').oninput = renderNodes;
  ['node-source-filter', 'node-status-filter', 'node-sort'].forEach((id) => {
    const field = $(`#${id}`);
    if (field) field.onchange = renderNodes;
  });
  if ($('#nav-toggle')) $('#nav-toggle').onclick = openNavigation;
  if ($('#nav-close')) $('#nav-close').onclick = closeNavigation;
  if ($('#nav-backdrop')) $('#nav-backdrop').onclick = closeNavigation;
  $('#clients-body')?.addEventListener('focusout', () => setTimeout(() => {
    if (can('router.clients')) renderClients(clientsData);
  }, 0));
  $('#nodes-body')?.addEventListener('focusout', () => setTimeout(() => {
    if (can('vpn.view')) renderNodes();
  }, 0));
  document.addEventListener?.('visibilitychange', () => {
    if (!document.hidden && authenticated) poll();
  });
  window.addEventListener?.('resize', () => {
    if (window.matchMedia?.('(min-width: 761px)').matches) closeNavigation();
    else syncOverlayInert();
  });
  syncOverlayInert();
}

function clearNodeFilters() {
  if ($('#node-source-filter')) { $('#node-source-filter').innerHTML = '<option value="">Все источники</option>'; $('#node-source-filter').value = ''; }
  if ($('#node-status-filter')) $('#node-status-filter').value = '';
  if ($('#node-sort')) $('#node-sort').value = 'label';
  $('#node-search').value = '';
}
function clearWorkspaceData() {
  chartHistory = [];
  clearNodeFilters();
}
