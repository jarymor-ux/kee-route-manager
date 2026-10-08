'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'internal/web/ui/static/app.js'), 'utf8');
const start = source.indexOf('async function loadEvents()');
const end = source.indexOf('\nfunction showTool', start);
assert(start >= 0 && end > start, 'loadEvents must be present');

function event(sequence) {
  return { sequence, timestamp: '2026-10-02T00:00:00Z', type: 'test', message: `event_${sequence}` };
}

function harness(read) {
  const element = { innerHTML: 'previous events' };
  const requests = [];
  const errors = [];
  const context = vm.createContext({
    Date,
    $: () => element,
    esc: String,
    toast: (message, bad) => errors.push({ message, bad }),
    api: async (request) => {
      const url = new URL(request, 'http://ui.test');
      const after = Number(url.searchParams.get('after'));
      const limit = Number(url.searchParams.get('limit'));
      requests.push({ after, limit });
      assert(requests.length <= 20, 'pagination must be bounded');
      return read(after, limit, requests.length);
    },
  });
  vm.runInContext(source.slice(start, end), context);
  return { element, requests, errors, load: () => vm.runInContext('loadEvents()', context) };
}

function visible(h) {
  return [...h.element.innerHTML.matchAll(/<strong>event_(\d+)<\/strong>/g)].map((match) => Number(match[1]));
}

function statusChecks() {
  const start = source.indexOf('function renderStatus(data)');
  const end = source.indexOf('\nfunction renderPool', start);
  assert(start >= 0 && end > start, 'renderStatus must be present');
  const elements = new Map();
  const select = (selector) => {
    if (!elements.has(selector)) {
      elements.set(selector, { textContent: '', className: '', style: {}, classList: { toggle() {} } });
    }
    return elements.get(selector);
  };
  const context = vm.createContext({ $: select, renderPool() {}, renderSources() {}, renderOperations() {}, can: () => true, fmtAge: () => '—' });
  vm.runInContext(source.slice(start, end), context);
  const render = (state, running = true) => {
    context.data = { version: 'test', xray_running: running, capabilities: {}, state };
    vm.runInContext('renderStatus(data)', context);
    return { status: select('#status-text').textContent, route: select('#route-mode').textContent, active: select('#active-node').textContent, dot: select('#status-dot').className };
  };
  const restored = { xray_configured: false, automatic_routing_paused: true, direct_mode: false, active_slot: -1, pool: [] };
  for (const running of [false, true]) {
    const paused = render(restored, running);
    assert.equal(paused.status, 'Управление приостановлено');
    assert.equal(paused.route, 'ПАУЗА');
    assert.equal(paused.dot, 'dot warn');
    assert.equal(paused.active, 'Запустите тестирование, чтобы возобновить управление');
  }
  const unconfigured = render({ ...restored, automatic_routing_paused: false });
  assert.equal(unconfigured.status, 'Маршрут не настроен');
  assert.equal(unconfigured.route, 'НЕ НАСТРОЕН');
  assert.equal(unconfigured.dot, 'dot warn');
  assert.equal(unconfigured.active, 'Запустите тестирование для выбора узла');
  const vpnState = { xray_configured: true, automatic_routing_paused: false, direct_mode: false, active_slot: 0, active_node_id: 'node', pool: [{ index: 0, node_id: 'node', label: 'Node' }] };
  const vpn = render(vpnState);
  assert.equal(vpn.status, 'VPN активен');
  assert.equal(vpn.route, 'VPN');
  assert.equal(vpn.active, 'Node');
  const direct = render({ ...vpnState, direct_mode: true, active_slot: -1, active_node_id: '' });
  assert.equal(direct.status, 'Прямой маршрут');
  assert.equal(direct.route, 'DIRECT');
  assert.equal(direct.dot, 'dot warn');
  assert.equal(direct.active, 'Трафик временно идёт напрямую');
  assert.equal(render(vpnState, false).status, 'Xray остановлен');
}

function appHarness() {
  const elements = new Map();
  const requests = [];
  const intervals = new Set();
  let reloads = 0;
  const handlers = {};
  const allPermissions = ['vpn.view', 'vpn.control', 'subscriptions.view', 'subscriptions.manage', 'router.view', 'router.clients', 'router.policy', 'router.wake', 'router.system', 'router.reboot', 'updates.manage', 'users.manage', 'events.view'];
  const select = (selector) => {
    if (!elements.has(selector)) {
      const classes = new Set(['hidden']);
      elements.set(selector, {
        id: selector.startsWith('#') ? selector.slice(1) : '', value: '', textContent: '', innerHTML: '', style: {}, dataset: {}, disabled: false,
        classList: {
          add: (name) => classes.add(name), remove: (name) => classes.delete(name), contains: (name) => classes.has(name),
          toggle: (name, force) => { if (force) classes.add(name); else classes.delete(name); },
        },
        setAttribute(name, value) { this[name] = value; },
        addEventListener(name, fn) { this[name] = fn; }, focus() {},
      });
    }
    return elements.get(selector);
  };
  const many = new Map();
  const selectAll = (selector) => many.get(selector) || [];
  let response = { status: 401, ok: false, data: { error: 'unauthorized' } };
  let confirmResult = true;
  const context = vm.createContext({
    document: { querySelector: select, querySelectorAll: selectAll }, navigator: {}, Date,
    window: { location: { hash: '', reload: () => { reloads++; } }, addEventListener: (name, handler) => { handlers[name] = handler; } },
    setTimeout() {}, clearInterval: (timer) => intervals.delete(timer),
    setInterval: (fn) => { intervals.add(fn); return fn; }, confirm: () => confirmResult,
    fetch: async (url, options) => {
      requests.push({ url, options });
      const current = typeof response === 'function' ? await response(url, options) : response;
      return { ...current, headers: { get: () => 'application/json' }, json: async () => current.data };
    },
  });
  context.allPermissions = allPermissions;
  vm.runInContext(source, context);
  vm.runInContext('permissions = new Set(allPermissions)', context);
  return { allPermissions, handlers, many, context, select, requests, intervals, reloads: () => reloads, confirm: (next) => { confirmResult = next; }, respond: (next) => { response = next; }, run: (code) => vm.runInContext(code, context) };
}

async function versionReloadChecks() {
  for (const interruption of ['none', 'expired', 'logout']) {
    const h = appHarness();
    await new Promise(setImmediate);
    h.run('authenticated = true; permissions = new Set(allPermissions)');
    const status = (version) => ({ version, state: { pool: [], sources: {} }, xray_running: true });
    const load = async (version) => {
      h.respond({ status: 200, ok: true, data: status(version) });
      await h.run('loadStatus()');
    };
    for (const absent of [undefined, null, '', '   ', 42]) await load(absent);
    assert.equal(h.reloads(), 0, 'missing version must not establish a reload baseline');
    await load('1.2.0-rc.1');
    await load('1.2.0-rc.1');
    await load(undefined);
    assert.equal(h.reloads(), 0, 'first version and unchanged version must not reload');

    if (interruption !== 'none') {
      if (interruption === 'expired') {
        h.respond({ status: 401, ok: false, data: { error: 'expired' } });
        await h.run('loadStatus()');
      } else {
        h.respond({ status: 200, ok: true, data: {} });
        await h.select('#logout').onclick();
      }
      await load('9.9.9');
      assert.equal(h.reloads(), 0, 'logged-out response must not reload or replace the baseline');
      h.respond((url) => ({ status: 200, ok: true, data: url.endsWith('/session')
        ? { csrf: 'new-session', permissions: h.allPermissions } : url.endsWith('/update/status') ? {} : status('1.2.0-rc.1') }));
      await h.run('session()');
      await new Promise(setImmediate);
      assert.equal(h.reloads(), 0, 'relogin to the same version must not reload');
    }
    h.run('clearInterval(pollTimer); pollTimer = setInterval(() => {}, 3000)');
    await load('1.2.0-rc.2');
    assert.equal(h.reloads(), 1, `new version after ${interruption} must fetch the new UI document`);
    assert.equal(h.intervals.size, 0, 'reload must stop the polling timer');
    await load('1.2.0-rc.2');
    await load('1.2.0-rc.3');
    assert.equal(h.reloads(), 1, 'pending document reload must not be requested repeatedly');
  }
}

async function appChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  assert.equal(h.select('#login').classList.contains('hidden'), false, 'expired session must require login');
  h.respond({ status: 200, ok: true, data: { accepted: true } });
  h.run("csrf = 'test-csrf'; permissions = new Set(allPermissions)");
  await h.run("api('/api/v1/actions/direct', { method: 'POST', body: '{}' })");
  let request = h.requests.at(-1);
  assert.equal(request.options.headers['X-KRM-CSRF'], 'test-csrf');
  assert.equal(request.options.headers['Content-Type'], 'application/json');
  assert.equal(request.options.credentials, 'same-origin');
  await h.run("api('/api/v1/status')");
  assert.equal(h.requests.at(-1).options.headers['X-KRM-CSRF'], undefined);
  h.respond({ status: 409, ok: false, data: { error: 'operation unavailable' } });
  await assert.rejects(() => h.run("api('/api/v1/actions/direct', { method: 'POST' })"), /operation unavailable/);
  h.respond({ status: 401, ok: false, data: { error: 'expired' } });
  h.run("pollTimer = setInterval(() => {}, 3000)");
  h.select('#password').value = 'never-retain-this';
  await assert.rejects(() => h.run("api('/api/v1/status')"), /Требуется вход/);
  assert.equal(h.intervals.size, 0, 'unauthorized response must stop polling');
  assert.equal(h.select('#password').value, '');

  h.run('permissions = new Set(allPermissions)');
  const injection = '<img src=x onerror="alert(1)">';
  h.context.injection = injection;
  h.run(`renderPool({ pool: [{ index: 0, label: injection, node_id: 'node', score: 1 }], active_slot: 0 });
    renderSources({ provider: { name: injection, status: 'healthy', last_error: injection, node_count: 1 } });
    subscriptionData = [{ id: 'primary', name: injection, url: 'https://user:password@example.test/private?token=secret', enabled: true, headers: { Authorization: 'Bearer secret' } }]; renderSubscriptions();
    nodesData = [{ label: injection, sources: [injection], network: 'ws', security: 'tls', measurement: {} }]; renderNodes();
    statusData = { capabilities: { wake_on_lan: true, client_policy: true } };
    renderClients([{ name: injection, mac: injection, ip: injection, connection_policy: injection }]);
    renderMetrics({ ports: [{ id: injection, link: injection, speed: injection }] });`);
  for (const selector of ['#pool', '#sources', '#subscriptions-body', '#nodes-body', '#clients-body', '#ports']) {
    const html = h.select(selector).innerHTML;
    assert(!html.includes('<img'), `${selector} must escape provider/router text`);
    assert(html.includes('&lt;img'), `${selector} must preserve escaped text`);
  }
  const subscriptionHTML = h.select('#subscriptions-body').innerHTML;
  assert(!subscriptionHTML.includes('password'), 'subscription URL credentials must not be rendered in the table');
  assert(!subscriptionHTML.includes('/private'), 'subscription URL path must not be rendered in the table');
  assert(!subscriptionHTML.includes('token=secret'), 'subscription URL query must not be rendered in the table');
  assert(!subscriptionHTML.includes('Bearer secret'), 'subscription header values must not be rendered in the table');

  h.run("csrf = 'subscription-csrf'; openSubscriptionEditor('primary')");
  h.select('#subscription-name').value = 'Updated';
  h.respond({ status: 200, ok: true, data: { ok: true } });
  await h.run('saveSubscription({ preventDefault() {} })');
  const saveRequest = h.requests.find((r) => r.url.endsWith('/subscriptions/save'));
  assert(saveRequest, 'subscription save request must be sent');
  assert.equal(saveRequest.options.method, 'POST');
  assert.equal(saveRequest.options.headers['X-KRM-CSRF'], 'subscription-csrf');
  assert.equal(JSON.parse(saveRequest.options.body).id, 'primary');
  assert.equal(h.select('#subscription-url').value, '', 'editor secrets must be cleared after save');

  h.select('#node-search').value = 'missing';
  h.run('renderNodes()');
  assert(h.select('#nodes-body').innerHTML.includes('Нет узлов'));
  h.run('showTool(injection)');
  assert.equal(h.select('#tool-output').textContent, injection, 'diagnostics must render as text');

  h.respond({ status: 200, ok: true, data: { available: true, latest_version: '2.0', current_version: '1.0' } });
  await h.run('checkUpdate()');
  assert(h.select('#update-apply').classList.contains('hidden'), 'legacy or launcherless discovery must not allow apply');
  assert(!h.requests.some((r) => r.url.endsWith('/update/apply')), 'update discovery must never apply');
  h.respond({ status: 200, ok: true, data: {} });
  await h.select('#logout').onclick();
  assert.equal(h.run('csrf'), '');
  assert.equal(h.select('#login').classList.contains('hidden'), false);
}

async function updateChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run("authenticated = true; permissions = new Set(allPermissions); csrf = 'update-csrf'");
  const check = { available: true, stage_supported: true, latest_version: '1.2.0', current_version: '1.1.0' };
  const state = { enabled: true, launcher: true, applying: false, phase: 'idle', current_version: '1.1.0', check };
  h.respond({ status: 200, ok: true, data: state });
  await h.run('loadUpdateStatus()');
  assert.equal(h.select('#update-apply').classList.contains('hidden'), false);
  assert.equal(h.select('#update-apply').disabled, false);
  assert(h.select('#update-status').textContent.includes('1.2.0'));

  assert(!h.requests.some((r) => r.url.endsWith('/update/check') || r.url.endsWith('/update/apply')), 'background status must never check or apply');

  h.confirm(false);
  await h.select('#update-apply').onclick();
  assert(!h.requests.some((r) => r.url.endsWith('/update/apply')), 'declined manual action must not apply');
  h.confirm(true);
  let finishApply;
  h.respond(() => new Promise((resolve) => { finishApply = resolve; }));
  const apply = h.select('#update-apply').onclick();
  await new Promise(setImmediate);
  assert.equal(h.select('#update-apply').disabled, true, 'in-flight request must disable repeated submission');
  await h.select('#update-apply').onclick();
  const requests = h.requests.filter((r) => r.url.endsWith('/update/apply'));
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.method, 'POST');
  assert.equal(requests[0].options.headers['X-KRM-CSRF'], 'update-csrf');
  assert.deepEqual(JSON.parse(requests[0].options.body), { version: '1.2.0' });
  finishApply({ status: 202, ok: true, data: { accepted: true } });
  await apply;
  assert.equal(h.select('#update-apply').disabled, true, 'accepted update remains busy until launcher status says otherwise');
  assert(h.select('#update-status').textContent.includes('Загрузка'));

  h.respond({ status: 200, ok: true, data: { ...state, applying: true, phase: 'trial' } });
  await h.run('loadUpdateStatus()');
  assert(h.select('#update-status').textContent.includes('Проверка'));
  assert.equal(h.select('#update-check').disabled, true);
  h.respond({ status: 200, ok: true, data: { ...state, phase: 'failed', last_error: '<script>failure</script>' } });
  await h.run('loadUpdateStatus()');
  assert(h.select('#update-status').textContent.includes('<script>failure</script>'), 'failure is rendered as text');
  assert.equal(h.select('#update-check').disabled, false);

  h.respond({ status: 200, ok: true, data: { ...state, current_version: '1.2.0', check: undefined } });
  await h.run('loadUpdateStatus()');
  assert.equal(h.select('#update-apply').classList.contains('hidden'), true, 'completed update must clear the old available release');
  assert(h.select('#update-status').textContent.includes('1.2.0'));

  for (const [result, label] of [['updated', 'Обновление установлено'], ['installed', 'Установка завершена'], ['rolled_back', 'Восстановлена предыдущая версия']]) {
    h.respond({ status: 200, ok: true, data: { ...state, check: undefined, last_result: result } });
    await h.run('loadUpdateStatus()');
    assert.equal(h.select('#update-status').textContent, label, 'launcher result must have a Russian label');
  }

  let finishStatus;
  h.respond(() => new Promise((resolve) => { finishStatus = resolve; }));
  const statusRead = h.run('loadUpdateStatus()');
  const before = h.requests.length;
  await h.run('loadUpdateStatus()');
  assert.equal(h.requests.length, before, 'slow status request must not overlap');
  finishStatus({ status: 502, ok: false, data: { error: 'launcher unavailable' } });
  await statusRead;
  assert.equal(h.select('#update-apply').disabled, true, 'unreachable launcher must revoke installation readiness');

  h.respond({ status: 401, ok: false, data: { error: 'expired' } });
  h.run('pollTimer = setInterval(() => {}, 3000)');
  await h.run('loadUpdateStatus()');
  assert.equal(h.intervals.size, 0);
  const count = h.requests.length;
  await h.run('loadUpdateStatus()');
  assert.equal(h.requests.length, count, 'logged-out status calls must not create an authentication storm');
}


async function updateTrialReconnectChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run("authenticated = true; permissions = new Set(allPermissions); csrf = 'update-csrf'");
  const check = { available: true, stage_supported: true, latest_version: '1.2.0', current_version: '1.1.0' };
  const state = { enabled: true, launcher: true, applying: false, phase: 'idle', current_version: '1.1.0', check };
  const controllerStatus = (version) => ({
    version,
    xray_running: true,
    capabilities: {},
    state: { pool: [], sources: {}, xray_configured: true, automatic_routing_paused: false, direct_mode: false, active_slot: -1 },
  });

  h.respond((url) => ({ status: 200, ok: true, data: url.endsWith('/update/status') ? state : controllerStatus('1.1.0') }));
  await h.run('loadUpdateStatus()');
  await h.run('loadStatus()');

  h.respond((url) => url.endsWith('/update/apply')
    ? { status: 202, ok: true, data: { accepted: true } }
    : { status: 200, ok: true, data: state });
  await h.select('#update-apply').onclick();
  assert.equal(h.run('Boolean(updateState?.applying)'), true, '202 Accepted must establish a local applying state');

  h.respond({ status: 503, ok: false, data: { error: 'temporary controller transition' } });
  await h.run('loadUpdateStatus()');
  await h.run('loadStatus()');

  const transitionalText = h.select('#update-status').textContent;
  assert(!transitionalText.includes('Обновление не выполнено'), 'temporary controller unavailability must not become an update failure');
  assert.match(transitionalText, /Проверка|переподключ/i, 'trial transition must keep a neutral reconnecting status');
  assert.equal(h.select('#update-apply').disabled, true, 'retry must stay disabled while the accepted update is transitional');
  assert.equal(h.run('Boolean(updateState?.applying)'), true, 'temporary polling failures must preserve the applying lifecycle');
  assert.equal(h.reloads(), 0);

  const recovered = { ...state, applying: false, phase: 'idle', current_version: '1.2.0', check: undefined, last_result: 'updated' };
  h.respond((url) => ({ status: 200, ok: true, data: url.endsWith('/update/status') ? recovered : controllerStatus('1.2.0') }));
  await h.run('loadUpdateStatus()');
  assert.equal(h.run('updateState.current_version'), '1.2.0', 'polling must accept launcher status again after the transition');
  await h.run('loadStatus()');
  assert.equal(h.reloads(), 1, 'new active controller version must trigger the normal UI reload flow');
}

async function updateFailureAfterReconnectChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run("authenticated = true; permissions = new Set(allPermissions); csrf = 'update-csrf'");
  const check = { available: true, stage_supported: true, latest_version: '1.2.0', current_version: '1.1.0' };
  const state = { enabled: true, launcher: true, applying: false, phase: 'idle', current_version: '1.1.0', check };

  h.respond({ status: 200, ok: true, data: state });
  await h.run('loadUpdateStatus()');
  h.respond({ status: 202, ok: true, data: { accepted: true } });
  await h.select('#update-apply').onclick();

  h.respond({ status: 503, ok: false, data: { error: 'temporary controller transition' } });
  await h.run('loadUpdateStatus()');
  assert(!h.select('#update-status').textContent.includes('Обновление не выполнено'));

  h.respond({ status: 200, ok: true, data: { ...state, phase: 'failed', last_error: 'candidate readiness failed' } });
  await h.run('loadUpdateStatus()');
  assert.equal(h.select('#update-status').textContent, 'Обновление не выполнено: candidate readiness failed', 'real launcher failure must remain visible after reconnect');
  assert.equal(h.select('#update-apply').disabled, false, 'retry may be offered only after launcher reports a terminal failure');
}

async function serviceWorkerChecks() {
  const worker = fs.readFileSync(path.join(root, 'internal/web/ui/static/sw.js'), 'utf8');
  const handlers = {};
  const cached = [];
  const openedCaches = [];
  const deletedCaches = [];
  let offline = false;
  const response = { ok: true, clone: () => ({ cached: true }) };
  const context = vm.createContext({
    URL,
    self: { location: { origin: 'https://ui.test' }, addEventListener: (name, handler) => { handlers[name] = handler; } },
    caches: {
      open: async (name) => {
        openedCaches.push(name);
        return { addAll: async (assets) => cached.push(...assets), put: async (request) => cached.push(request.url) };
      },
      keys: async () => ['krm-ui-old', 'krm-ui-rc2', 'krm-ui-static-v1'],
      delete: async (name) => { deletedCaches.push(name); return true; },
      match: async () => ({ offline: true }),
    },
    fetch: async () => { if (offline) throw new Error('offline'); return response; },
  });
  vm.runInContext(worker, context);
  let installation;
  handlers.install({ waitUntil: (promise) => { installation = promise; } });
  await installation;
  assert.deepEqual(cached, ['/', '/assets/app.css', '/assets/app.js', '/manifest.webmanifest']);
  assert.deepEqual(openedCaches, ['krm-ui-static-v1']);

  let activation;
  handlers.activate({ waitUntil: (promise) => { activation = promise; } });
  await activation;
  assert.deepEqual(deletedCaches.sort(), ['krm-ui-old', 'krm-ui-rc2']);
  assert(!deletedCaches.includes('krm-ui-static-v1'), 'active service-worker cache must be preserved');

  for (const [method, url] of [['GET', 'https://ui.test/api/v1/status'], ['POST', 'https://ui.test/'], ['GET', 'https://other.test/'], ['GET', 'https://ui.test/?secret=value']]) {
    let intercepted = false;
    handlers.fetch({ request: { method, url }, respondWith: () => { intercepted = true; } });
    assert.equal(intercepted, false, `${method} ${url} must bypass cache`);
  }
  for (const isOffline of [false, true]) {
    offline = isOffline;
    let fetched;
    const background = [];
    handlers.fetch({ request: { method: 'GET', url: 'https://ui.test/assets/app.js' }, respondWith: (promise) => { fetched = promise; }, waitUntil: (promise) => background.push(promise) });
    const got = await fetched;
    await Promise.all(background);
    assert.equal(isOffline ? got.offline : got.ok, true);
  }
}

async function navigationPermissionChecks() {
  const html = fs.readFileSync(path.join(root, 'internal/web/ui/static/index.html'), 'utf8');
  assert.equal((html.match(/class="group-toggle"/g) || []).length, 2, 'only router and VPN top-level groups');
  const ids = [...html.matchAll(/\bid="([^"]+)"/g)].map((match) => match[1]);
  assert.equal(new Set(ids).size, ids.length, 'page elements must have unique IDs');
  const h = appHarness();
  await new Promise(setImmediate);
  h.run('authenticated = true; permissions = new Set(allPermissions)');
  const pages = ['router', 'devices', 'interfaces', 'system', 'users', 'overview', 'subscriptions', 'nodes', 'testing', 'events'];
  const buttons = pages.map((page) => { const element = h.select(`#nav-${page}`); element.dataset.tab = page; return element; });
  const sections = pages.map((page) => h.select(`#tab-${page}`));
  h.many.set('#tabs [data-tab]', buttons);
  h.many.set('.tab', sections);
  h.respond((url) => ({ status: 200, ok: true, data: url.endsWith('/users') ? { users: [], permissions: h.allPermissions, roles: {} } : url.endsWith('/nodes') ? [] : {} }));
  h.run("navigate('users')");
  assert.equal(h.context.window.location.hash, 'users', 'navigation updates deep link');
  assert.equal(h.select('#tab-users').classList.contains('active'), true);
  assert.equal(h.select('#tab-router').classList.contains('active'), false);
  assert.equal(h.select('#tabs .group-toggle[data-group="router"]')['aria-expanded'], 'true');
  h.context.window.location.hash = '#nodes';
  h.handlers.hashchange();
  assert.equal(h.run('activePage'), 'nodes', 'browser hash changes restore selected page');
  assert.equal(h.select('#tab-nodes').classList.contains('active'), true);
  h.run("toggleGroup('vpn', false)");
  assert.equal(h.select('#menu-vpn').classList.contains('hidden'), true, 'submenu can collapse');
  h.context.window.location.hash = '#interfaces';
  h.run('startPolling()');
  assert.equal(h.run('activePage'), 'interfaces', 'initial deep link is restored after login');
  await new Promise(setImmediate);

  h.respond({ status: 200, ok: true, data: {} });
  h.run("acceptSession({csrf:'fresh',permissions:['vpn.view','subscriptions.view'],user:{id:'viewer'}})");
  assert.equal(h.run('activePage'), 'overview', 'revoked page falls back to an authorized page');
  h.run("renderPool({pool:[{index:0,node_id:'node',label:'node'}]}); renderClients([{mac:'aa'}]);");
  assert(!h.select('#pool').innerHTML.includes('slot-switch'), 'viewer has no switch button');
  assert(!h.select('#clients-body').innerHTML.includes('class="policy"'), 'viewer has no policy control');
  assert(!h.select('#clients-body').innerHTML.includes('class="ghost compact wake"'), 'viewer has no WOL control');
  const before = h.requests.length;
  await assert.rejects(() => h.run("api('/api/v1/users')"), /Недостаточно прав/);
  await assert.rejects(() => h.run("api('/api/v1/actions/direct', {method:'POST'})"), /Недостаточно прав/);
  assert.equal(h.requests.length, before, 'denied endpoints are never fetched');
  h.run("subscriptionData = [{id:'test',url:'https://private',headers:{Authorization:'secret'},enabled:true}];renderSubscriptions();openSubscriptionEditor('test')");
  assert(!h.select('#subscriptions-body').innerHTML.includes('subscription-edit'));
  assert.equal(h.select('#subscription-modal').classList.contains('hidden'), true, 'read-only subscription never opens secret editor');

  let finish;
  h.respond(() => new Promise((resolve) => { finish = resolve; }));
  const inFlight = h.run("api('/api/v1/subscriptions')");
  h.run("acceptSession({csrf:'new',permissions:['vpn.view']})");
  finish({status:200,ok:true,data:{sources:[{url:'secret'}]}});
  await assert.rejects(() => inFlight, /Доступ изменён/, 'response started before revoke must be discarded');
  assert.equal(h.run('subscriptionData.length'), 0);

  const requested = [];
  h.respond((url) => {
    requested.push(url);
    return {status:200,ok:true,data:url.endsWith('/session') ? {csrf:'next',permissions:['vpn.view']} : {version:'test',state:{pool:[],sources:{}}}};
  });
  h.run("activePage='overview'");
  await h.run('poll()');
  assert(requested[0].endsWith('/session'), 'each polling round refreshes permissions first');
  assert(!requested.some((url) => url.includes('/router/') || url.includes('/update/') || url.includes('/users')), 'polling never loads unauthorized domains');
  h.run("showTool('sensitive');usersData=[{username:'secret'}];clientsData=[{name:'private'}];showLogin()");
  assert.equal(h.select('#tool-output').textContent, '');
  assert.equal(h.run('usersData.length + clientsData.length'), 0, 'logout clears private caches');
  assert.equal(h.run('csrf'), '');
}

async function dashboardAndOperationChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run('permissions = new Set(allPermissions)');
  h.run('renderMetrics({})');
  assert(h.select('#traffic').textContent.includes('↓ — · ↑ —'), 'missing counters cannot be presented as idle traffic');
  assert(h.select('#metrics-freshness').textContent.includes('устарели'));
  h.context.now = new Date().toISOString();
  h.run('renderMetrics({updated_at:now,traffic_available:true,rx_mbps:0,tx_mbps:0,cpu_percent:0,ram_percent:0})');
  assert.equal(h.select('#traffic').textContent, '↓ 0.0 · ↑ 0.0 Мбит/с', 'valid idle counters remain zero');
  assert(!h.select('#metrics-freshness').textContent.includes('устарели'));
  h.run('renderMetrics({updated_at:now,stale:true,traffic_available:true,rx_mbps:0,tx_mbps:0})');
  assert(h.select('#metrics-freshness').textContent.includes('устарели'), 'a recent failed observation remains stale');
  h.run('renderMetrics({updated_at:now,traffic_available:false,rx_mbps:0,tx_mbps:0})');
  assert(h.select('#traffic').textContent.includes('↓ —'), 'unavailable adapter counters are not treated as zero');
  h.run('renderCharts([])');
  assert(h.select('#usage-chart').innerHTML.includes('недоступны'));
  h.context.samples = [{ updated_at: new Date(Date.now()-20000).toISOString(), cpu_percent:30 }, {updated_at:new Date(Date.now()-10000).toISOString(), cpu_percent:null}, {updated_at:new Date().toISOString(), cpu_percent:40}];
  h.run('renderCharts(samples)');
  assert.equal((h.select('#usage-chart').innerHTML.match(/<circle/g) || []).length, 2, 'missing sample breaks the chart line');
  assert(!h.select('#usage-chart').innerHTML.includes('NaN'));
  h.run("renderStatus({version:'test',state:{pool:[],sources:{}},capabilities:{reboot:true,system_logs:true},operation:{type:'benchmark',status:'running'},operations:[{type:'benchmark',status:'running',message:'testing'},{type:'client-policy',status:'running',message:'policy'}]})");
  assert(h.select('#operations-list').innerHTML.includes('Политика устройства'));
  h.run("renderOperations([{type:'benchmark',status:'canceled'}])");
  assert(h.select('#operations-list').innerHTML.includes('Отменена'), 'explicit cancellation has a localized status');
  h.run("renderStatus({state:{active_slot:0,pool:[{index:0,node_id:'active',healthy:true},{index:1,node_id:'bad',healthy:false},{index:2,node_id:'good',healthy:true}]},operations:[{type:'benchmark',status:'running'}]})");
  assert.equal(h.select('#pool-count').textContent, '1', 'unhealthy reserves cannot be labeled ready');
  assert.equal(h.select('#benchmark').disabled, true, 'duplicate benchmark start is prevented');
  assert.equal(h.select('#direct').disabled, false, 'benchmark does not block independent manual routing');
  assert.equal(h.select('#benchmark-cancel').classList.contains('hidden'), false);
  h.run("permissions = new Set(['vpn.view']); renderStatus({state:{pool:[],sources:{}},operations:[{type:'benchmark',status:'running'}]})");
  assert.equal(h.select('#benchmark-cancel').classList.contains('hidden'), true, 'cancellation requires VPN control permission');
}

async function userManagementChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run('authenticated=true;permissions=new Set(allPermissions)');
  const account = {id:'alice',username:'alice',enabled:true,permissions:['vpn.view'],updated_at:'2026-10-08T10:11:12.123456789Z'};
  const userResponse = {users:[account],permissions:h.allPermissions,roles:{viewer:['vpn.view','router.view'],admin:h.allPermissions}};
  h.respond((url) => ({status:200,ok:true,data:url.endsWith('/users') ? userResponse : url.endsWith('/session') ? {csrf:'test',permissions:h.allPermissions,user:{id:'admin'}} : {ok:true}}));
  await h.run('loadUsers()');
  h.run("openUserEditor('alice')");
  assert.equal(h.select('#user-name').value, 'alice');
  assert.equal(h.select('#user-password').required, false, 'editing an account need not change password');
  const view = h.select('#permission-view'); view.value='vpn.view';view.checked=true;
  const control = h.select('#permission-control'); control.value='vpn.control';control.checked=false;
  const router = h.select('#permission-router');router.value='router.view';router.checked=false;
  h.many.set('#user-permissions input',[view,control,router]);
  h.select('#user-role').value='viewer';
  h.select('#user-role').onchange();
  assert.equal(router.checked,true);
  assert.equal(control.checked,false);
  h.context.newerVersion='2026-10-08T10:12:00.987654321Z';
  h.run('usersData[0] = {...usersData[0], updated_at:newerVersion}');
  await h.run('saveUser({preventDefault(){}})');
  let saved = h.requests.find((request)=>request.url.endsWith('/users/save'));
  assert.deepEqual(JSON.parse(saved.options.body),{id:'alice',username:'alice',enabled:true,permissions:['vpn.view','router.view'],expected_updated_at:account.updated_at});
  assert.equal(h.select('#user-password').value,'', 'password is cleared after a successful save');
  h.run('openUserEditor()');
  assert.equal(h.select('#user-password').required,true);
  h.select('#user-name').value='bob';h.select('#user-password').value='longpassword';
  await h.run('saveUser({preventDefault(){}})');
  saved=h.requests.filter((request)=>request.url.endsWith('/users/save')).at(-1);
  assert.equal(JSON.parse(saved.options.body).password,'longpassword');
  assert.equal(JSON.parse(saved.options.body).id,undefined);
  assert.equal(JSON.parse(saved.options.body).expected_updated_at,undefined, 'new user omits edit precondition');
  await h.run("toggleUser('alice')");
  assert.equal(JSON.parse(h.requests.filter((request)=>request.url.endsWith('/users/save')).at(-1).options.body).enabled,false);
  assert.equal(JSON.parse(h.requests.filter((request)=>request.url.endsWith('/users/save')).at(-1).options.body).expected_updated_at,h.context.newerVersion);
  await h.run("deleteUser('alice')");
  assert.deepEqual(JSON.parse(h.requests.find((request)=>request.url.endsWith('/users/delete')).options.body),{id:'alice',expected_updated_at:h.context.newerVersion});
}

async function errorTranslationChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run('permissions = new Set(allPermissions)');
  const messages = {busy:'несовместимая',canceled:'отменена',settings_changed:'не применён',conflict:'конфликтует',unavailable:'недоступна'};
  for (const [code, message] of Object.entries(messages)) {
    h.respond({status:409,ok:false,data:{error:'operation rejected',code}});
    await assert.rejects(() => h.run("api('/api/v1/actions/direct',{method:'POST'})"), (error) => error.message.includes(message));
  }
  h.respond({status:409,ok:false,data:{error:'user changed; reload before saving'}});
  await assert.rejects(() => h.run("api('/api/v1/users/save',{method:'POST'})"), /откройте редактор заново/);
}

async function main() {
  await errorTranslationChecks();
  await navigationPermissionChecks();
  await dashboardAndOperationChecks();
  await userManagementChecks();
  await appChecks();
  await versionReloadChecks();
  await updateChecks();
  await updateTrialReconnectChecks();
  await updateFailureAfterReconnectChecks();
  await serviceWorkerChecks();
  statusChecks();
  for (const count of [0, 301, 1000, 2000]) {
    const events = Array.from({ length: count }, (_, index) => event(index + 1));
    const h = harness((after, limit) => events.filter((item) => item.sequence > after).slice(0, limit));
    await h.load();
    assert.deepEqual(visible(h), events.slice(-300).reverse().map((item) => item.sequence), `latest events for ${count} retained`);
    assert.deepEqual(h.errors, []);
  }

  // Refresh starts from retained history again, including when older records
  // have been compacted away and sequence numbers have gaps.
  let events = Array.from({ length: 1300 }, (_, index) => event(10 + index * 3));
  const h = harness((after, limit) => events.filter((item) => item.sequence > after).slice(0, limit));
  await h.load();
  events = events.slice(1000).concat([event(5000), event(5010)]);
  await h.load();
  assert.deepEqual(visible(h), events.slice(-300).reverse().map((item) => item.sequence));
  assert.deepEqual(h.errors, []);

  // Compaction during pagination must not assume contiguous sequences.
  let compacted = Array.from({ length: 2000 }, (_, index) => event(index + 1));
  const truncation = harness((after, limit, call) => {
    if (call === 2) compacted = compacted.slice(1500).concat([event(3000)]);
    return compacted.filter((item) => item.sequence > after).slice(0, limit);
  });
  await truncation.load();
  assert.deepEqual(visible(truncation), compacted.slice(-300).reverse().map((item) => item.sequence));

  const failure = harness((after, limit, call) => {
    if (call === 2) throw new Error('upstream unavailable');
    return Array.from({ length: limit }, (_, index) => event(after + index + 1));
  });
  await failure.load();
  assert.equal(failure.element.innerHTML, 'previous events');
  assert.equal(failure.errors.length, 1);
  assert.equal(failure.errors[0].bad, true);

  const stuck = harness((after, limit) => Array.from({ length: limit }, (_, index) => event(index + 1)));
  await stuck.load();
  assert(stuck.requests.length <= 2, 'nonadvancing cursor must terminate promptly');
  assert.equal(stuck.element.innerHTML, 'previous events');
  assert.equal(stuck.errors.length, 1);

  const endless = harness((after, limit) => Array.from({ length: limit }, (_, index) => event(after + index + 1)));
  await endless.load();
  assert(endless.requests.length <= 10, 'continuously growing history must have a request bound');
  assert.equal(endless.element.innerHTML, 'previous events');
  assert.equal(endless.errors.length, 1);

  console.log('UI authentication, permissions, navigation, users, dashboards, operations, updates and event pagination checks passed.');
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
