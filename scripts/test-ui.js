'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.resolve(__dirname, '..');
const staticRoot = path.join(root, 'internal/web/ui/static');
const htmlSource = fs.readFileSync(path.join(staticRoot, 'index.html'), 'utf8');
const declaredScripts = [...htmlSource.matchAll(/<script\b[^>]*\bsrc=["']\/assets\/([a-z-]+\.js)["'][^>]*>/g)].map((match) => match[1]);
const editingOrder = ['app.js', 'workspace.js', 'charts.js', 'dialogs.js', 'settings.js', 'panel.js', 'boot.js'];
const scriptFiles = declaredScripts.includes('boot.js') ? declaredScripts : editingOrder.filter((name) => fs.existsSync(path.join(staticRoot, name)));
const source = scriptFiles.map((name) => fs.readFileSync(path.join(staticRoot, name), 'utf8')).join('\n');
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
  const start = source.indexOf('function renderStatus(data, force = false)');
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
  const assignments = [];
  const blobURLs = new Set();
  const revokedBlobs = [];
  class TestURL extends URL {
    static createObjectURL() { const value = `blob:ui-test-${blobURLs.size + revokedBlobs.length}`; blobURLs.add(value); return value; }
    static revokeObjectURL(value) { blobURLs.delete(value); revokedBlobs.push(value); }
  }
  const handlers = {};
  const storage = new Map();
  const document = { querySelector: (selector) => select(selector), querySelectorAll: (selector) => selectAll(selector), hidden: false, activeElement: null, addEventListener: (name, handler) => { handlers[`document:${name}`] = handler; } };
  const allPermissions = ['vpn.view', 'vpn.control', 'subscriptions.view', 'subscriptions.manage', 'router.view', 'router.clients', 'router.policy', 'router.wake', 'router.system', 'router.reboot', 'updates.manage', 'users.manage', 'config.manage', 'events.view'];
  const select = (selector) => {
    if (!elements.has(selector)) {
      const classes = new Set(['hidden']);
      elements.set(selector, {
        id: selector.startsWith('#') ? selector.slice(1) : '', value: '', textContent: '', innerHTML: '', style: {}, dataset: {}, disabled: false,
        classList: {
          add: (name) => classes.add(name), remove: (name) => classes.delete(name), contains: (name) => classes.has(name),
          toggle: (name, force) => { if (force) classes.add(name); else classes.delete(name); },
        },
        setAttribute(name, value) { this[name] = value; }, removeAttribute(name) { delete this[name]; },
        addEventListener(name, fn) { this[name] = fn; },
        getAttribute(name) { return this[name] ?? null; },
        querySelectorAll(selector) { return many.get(`${this.id} ${selector}`) || selectAll(selector); },
        contains(element) { for (let item = element; item; item = item.parentElement) if (item === this) return true; return false; },
        closest(selector) { return selector === '.modal' ? this.modal || null : null; },
        getClientRects() { return [{}]; }, isConnected: true,
        focus() { document.activeElement = this; },
      });
    }
    return elements.get(selector);
  };
  const many = new Map();
  const selectAll = (selector) => many.get(selector) || [];
  let response = { status: 401, ok: false, data: { error: 'unauthorized' } };
  let confirmResult = true;
  const context = vm.createContext({
    document, navigator: {}, Date, URL: TestURL, Blob,
    localStorage: { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, String(value)) },
    matchMedia: () => ({ matches: false, addEventListener() {} }),
    window: { matchMedia: () => ({ matches: false, addEventListener() {} }), location: { hash: '', origin: 'https://alice.jopa', assign: (url) => assignments.push(url), reload: () => { reloads++; } }, addEventListener: (name, handler) => { handlers[name] = handler; } },
    setTimeout() {}, clearTimeout() {}, clearInterval: (timer) => intervals.delete(timer),
    setInterval: (fn) => { intervals.add(fn); return fn; }, confirm: () => confirmResult,
    fetch: async (url, options) => {
      requests.push({ url, options });
      const current = typeof response === 'function' ? await response(url, options) : response;
      return { ...current, headers: { get: () => 'application/json' }, json: async () => current.data };
    },
  });
  document.body = select('body'); document.documentElement = select('html');
  context.allPermissions = allPermissions;
  for (const name of scriptFiles) vm.runInContext(fs.readFileSync(path.join(staticRoot, name), 'utf8'), context, { filename: name });
  vm.runInContext('permissions = new Set(allPermissions)', context);
  return { document, allPermissions, handlers, many, assignments, blobURLs, revokedBlobs, context, select, requests, intervals, reloads: () => reloads, confirm: (next) => { confirmResult = next; }, respond: (next) => { response = next; }, run: (code) => vm.runInContext(code, context) };
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
  assert.deepEqual(cached, ['/', '/assets/app.css', ...scriptFiles.map((name) => `/assets/${name}`), '/manifest.webmanifest']);
  const activeCache = vm.runInContext('CACHE', context);
  assert.deepEqual(openedCaches, [activeCache]);

  let activation;
  handlers.activate({ waitUntil: (promise) => { activation = promise; } });
  await activation;
  assert.deepEqual(deletedCaches.sort(), ['krm-ui-old', 'krm-ui-rc2', 'krm-ui-static-v1'].filter((name) => name !== activeCache).sort());
  assert(!deletedCaches.includes(activeCache), 'active service-worker cache must be preserved');

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

async function updateChannelChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run("authenticated=true;permissions=new Set(allPermissions);csrf='channel-csrf'");
  const oldCheck = {channel:'rc',available:true,stage_supported:true,current_version:'2.0.0-rc.2',latest_version:'2.0.0-rc.3'};
  const rc = {channel:'rc',channel_switch_supported:true,launcher:true,enabled:true,applying:false,phase:'idle',current_version:'2.0.0-rc.2',check:oldCheck};
  const stable = {...rc,channel:'stable',check:undefined};
  h.respond({status:200,ok:true,data:rc});
  await h.run('loadUpdateStatus()');
  assert.equal(h.select('#update-channel').value,'rc');
  assert.equal(h.select('#update-channel').disabled,false);
  assert.equal(h.select('#update-apply').disabled,false);

  let finishCheck, finishStatus, finishSwitch;
  h.respond((url) => new Promise((resolve) => {
    if (url.endsWith('/check')) finishCheck=resolve;
    else if (url.endsWith('/status')) finishStatus=resolve;
    else if (url.endsWith('/channel')) finishSwitch=resolve;
    else throw new Error(`unexpected request ${url}`);
  }));
  const check = h.run('checkUpdate()');
  const status = h.run('loadUpdateStatus()');
  h.select('#update-channel').value='stable';
  const switching = h.select('#update-channel').onchange();
  await new Promise(setImmediate);
  const request=h.requests.filter((item)=>item.url.endsWith('/channel')).at(-1);
  assert.equal(request.options.method,'POST');
  assert.equal(request.options.headers['X-KRM-CSRF'],'channel-csrf');
  assert.deepEqual(JSON.parse(request.options.body),{channel:'stable'});
  assert.equal(h.select('#update-channel').disabled,true,'channel mutation cannot overlap');
  assert.equal(h.select('#update-check').disabled,true);
  assert.equal(h.run('pendingUpdate'),null,'switch immediately removes the previously discovered release');
  const count=h.requests.length;
  await h.run("switchUpdateChannel('rc');checkUpdate();applyUpdate()");
  assert.equal(h.requests.length,count,'check/apply/second switch are blocked while switching');
  finishSwitch({status:200,ok:true,data:stable});
  await switching;
  finishCheck({status:200,ok:true,data:oldCheck});
  finishStatus({status:200,ok:true,data:rc});
  await Promise.all([check,status]);
  assert.equal(h.run('updateState.channel'),'stable','stale poll cannot undo saved channel');
  assert.equal(h.run('pendingUpdate'),null,'old-channel check response is discarded');
  assert.equal(h.select('#update-channel').value,'stable');
  assert.equal(h.run('updateState.current_version'),'2.0.0-rc.2','channel change does not change installed version');
  assert(h.select('#tool-output').textContent.includes('Установленная версия не изменена'));
  assert(!h.requests.some((item)=>item.url.endsWith('/apply')),'channel switching never installs');

  h.respond({status:200,ok:true,data:{...stable,check:oldCheck}});
  await h.run('loadUpdateStatus()');
  assert.equal(h.run('pendingUpdate'),null,'status with old-channel cached check does not offer it');
  assert.equal(h.select('#update-apply').classList.contains('hidden'),true);
  const releaseCheck={...oldCheck,channel:'stable',latest_version:'2.0.0'};
  h.respond({status:200,ok:true,data:releaseCheck});
  await h.run('checkUpdate()');
  assert.equal(h.run('pendingUpdate.latest_version'),'2.0.0');
  assert.equal(h.select('#update-apply').disabled,false,'new channel supports a separately requested install');
  h.respond({status:202,ok:true,data:{accepted:true}});
  await h.run('applyUpdate()');
  const applyRequest=h.requests.filter((item)=>item.url.endsWith('/apply')).at(-1);
  assert.deepEqual(JSON.parse(applyRequest.options.body),{version:'2.0.0',channel:'stable'},'channel-aware install binds confirmation to discovered channel');
  assert.equal(h.select('#update-channel').disabled,true);

  h.respond({status:200,ok:true,data:{...stable,applying:true,phase:'trial'}});
  await h.run('loadUpdateStatus()');
  assert.equal(h.select('#update-channel').disabled,true,'installation prevents channel switching');
  const applyingCount=h.requests.length;
  await h.run("switchUpdateChannel('rc')");
  assert.equal(h.requests.length,applyingCount);
  for (const unsupported of [{launcher:true,current_version:'old'}, {...stable,channel_switch_supported:false}]) {
    h.respond({status:200,ok:true,data:unsupported});
    await h.run('loadUpdateStatus()');
    assert.equal(h.select('#update-channel').disabled,true);
    assert.equal(h.select('#update-channel-hint').textContent,'Для смены канала требуется обновить стабильный launcher');
    const before=h.requests.length;
    await h.run("switchUpdateChannel('rc')");
    assert.equal(h.requests.length,before,'legacy/custom discovery cannot mutate channel');
  }
  for (const [reason, hint] of [['disabled','Обновления отключены'],['manual_urls','Канал задан URL манифеста; переключение доступно при поиске релизов GitHub']]) {
    h.respond({status:200,ok:true,data:{...stable,channel_switch_supported:false,channel_switch_reason:reason}});
    await h.run('loadUpdateStatus()');
    assert.equal(h.select('#update-channel-hint').textContent,hint);
    assert.equal(h.select('#update-channel').disabled,true);
  }
  const olderCheck={available:false,stage_supported:true,current_version:'2.0.0-rc.2',latest_version:'1.0.0',manifest:{channel:'stable'}};
  h.respond({status:200,ok:true,data:{...stable,check:olderCheck}});
  await h.run('loadUpdateStatus()');
  assert.equal(h.run('pendingUpdate.latest_version'),'1.0.0','manifest channel supports mixed-format checks');
  assert(h.select('#update-status').textContent.includes('Более новой версии в выбранном канале нет'));
  assert(h.select('#update-status').textContent.includes('Установлена 2.0.0-rc.2'));
  assert(h.select('#update-status').textContent.includes('Последняя опубликованная: 1.0.0'));
  assert.equal(h.select('#update-apply').classList.contains('hidden'),true,'older latest release is never installable');
  h.respond({status:200,ok:true,data:olderCheck});
  await h.run('checkUpdate()');
  assert(h.select('#tool-output').textContent.includes('Более новой версии в выбранном канале нет'));
  h.run("updateState={channel:'stable',channel_switch_supported:true};acceptSession({permissions:['router.view']})");
  assert.equal(h.select('#update-channel').disabled,true,'revoked update permissions disable channel selector');
  const deniedCount=h.requests.length;
  await h.run("switchUpdateChannel('rc')");
  assert.equal(h.requests.length,deniedCount);
}

async function settingsEditorChecks() {
  const h = appHarness();
  await new Promise(setImmediate);
  h.run("authenticated=true;permissions=new Set(['config.manage'])");
  // Build a complete fixture through the documented fields, then preserve the router's cadence/cache choice.
  const settings = h.run(`(() => {
    const value = {};
    for (const [, prefix, fields] of settingsSections) {
      let group = value;
      for (const key of prefix.split('.')) group = group[key] ||= {};
      for (const [name, , type] of fields) group[name] = type === 'boolean' ? false : ['number', 'decimal'].includes(type) ? 2 : type === 'bytes' ? '64 KiB' : type === 'durations' ? ['30s', '1m'] : type === 'text' ? '' : '5m';
    }
    value.benchmark.full_interval = '5m';
    return value;
  })()`);
  const baseline = { settings, revision: 'original', pool_size: 5, apply: { status: 'idle' } };
  h.respond({status:200,ok:true,data:baseline});
  await h.run('loadSettings()');
  assert.equal(h.select('#setting-benchmark-full_interval').value, '5m');
  assert.equal(h.select('#setting-subscriptions-cache_enabled').checked, false);
  assert(h.select('#settings-fields').innerHTML.includes('Изменение размера через панель пока недоступно'));
  assert.equal(h.select('#settings-save').disabled, true);
  assert.equal(h.run("permissionLabels['config.manage']"), 'Редактирование настроек');
  const inputs = h.run('settingsSections.flatMap(([,prefix,fields])=>fields.map(([name])=>settingsFieldID(`${prefix}.${name}`)))').map((id)=>h.select(`#${id}`));
  h.many.set('#settings-fields input',inputs);
  h.select('#setting-benchmark-full_interval').value='10m';
  h.run("settingsChanged();activePage='settings'");
  assert.equal(h.run('settingsDirty'),true);
  assert.equal(h.select('#settings-save').disabled,false);
  const count=h.requests.filter((request)=>request.url.includes('/settings')).length;
  await h.run("loadPage('settings');loadSettings()");
  assert.equal(h.requests.filter((request)=>request.url.includes('/settings')).length,count,'background refresh cannot replace a dirty draft');
  assert.equal(h.select('#setting-benchmark-full_interval').value,'10m');
  let prevented=false;
  h.handlers.beforeunload({preventDefault(){prevented=true;}});
  assert.equal(prevented,true,'browser exit warns about unsaved changes');
  h.confirm(false);
  h.run("navigate('router')");
  assert.equal(h.run('activePage'),'settings','rejected navigation retains the draft');
  await h.run('loadSettings(true)');
  assert.equal(h.requests.filter((request)=>request.url.includes('/settings')).length,count,'rejected refresh also retains the draft');
  h.confirm(true);

  h.respond({status:200,ok:true,data:{valid:true}});
  await h.run('validateSettings()');
  const validation = h.requests.at(-1);
  assert(validation.url.endsWith('/settings/validate'));
  assert.equal(JSON.parse(validation.options.body).settings.benchmark.full_interval,'10m');
  assert.equal(JSON.parse(validation.options.body).revision,'original');
  assert.equal(h.run('settingsDirty'),true,'validation does not apply or erase edits');

  h.respond({status:409,ok:false,data:{code:'settings_conflict',error:'conflict'}});
  await h.run('saveSettings({preventDefault(){}})');
  assert(h.select('#settings-error').textContent.includes('уже изменены'));
  assert.equal(h.select('#setting-benchmark-full_interval').value,'10m');
  assert.equal(h.select('#settings-save').disabled,true,'revision conflict requires a refresh before another save');
  h.respond({status:200,ok:true,data:baseline});
  await h.run('loadSettings(true)');
  h.select('#setting-benchmark-full_interval').value='10m';h.run('settingsChanged()');
  let finishSave;
  h.respond((url)=>url.endsWith('/save') ? new Promise((resolve)=>{finishSave=resolve;}) : {status:200,ok:true,data:{...baseline,apply:{status:'applying',revision:'candidate'}}});
  const save=h.run('saveSettings({preventDefault(){}})');
  assert.equal(h.select('#settings-save').disabled,true);
  assert(inputs.every((input)=>input.disabled),'applying freezes draft fields');
  const pendingCount=h.requests.length;
  await h.run('saveSettings({preventDefault(){}});validateSettings();loadSettings(true)');
  assert.equal(h.requests.length,pendingCount,'save, validate and refresh cannot overlap an accepted save');
  finishSave({status:202,ok:true,data:{accepted:true,changed:true,revision:'candidate'}});
  await save;
  assert.equal(h.run('settingsSaving'),true,'apply readiness is required after acceptance');
  h.respond({status:200,ok:true,data:{...baseline,revision:'other',apply:{status:'applied',revision:'other'}}});
  await h.run('pollSettingsApply(settingsGeneration)');
  assert.equal(h.run('settingsSaving'),true,"another user's applied revision cannot complete this save");
  const changed=JSON.parse(JSON.stringify(settings));changed.benchmark.full_interval='10m0s';
  h.respond({status:200,ok:true,data:{...baseline,settings:changed,revision:'candidate',apply:{status:'applied',revision:'candidate'}}});
  await h.run('pollSettingsApply(settingsGeneration)');
  assert.equal(h.run('settingsDirty'),false);
  assert.equal(h.run('settingsSaving'),false);
  assert.equal(h.select('#setting-benchmark-full_interval').value,'10m0s','server-normalized strings are accepted by revision');
  assert(h.select('#settings-status').textContent.includes('применены'));
  assert.equal(h.reloads(),0,'settings saves do not trigger an unconditional document reload');

  h.select('#setting-benchmark-full_interval').value='20m';h.run('settingsChanged()');
  h.respond((url)=>({status:200,ok:true,data:url.endsWith('/save') ? {changed:true,accepted:true,revision:'rollback-candidate'} : {...baseline,apply:{status:'rolled_back',revision:'rollback-candidate'}}}));
  await h.run('saveSettings({preventDefault(){}})');
  assert.equal(h.run('settingsSaving'),false);
  assert.equal(h.select('#setting-benchmark-full_interval').value,'20m','rollback retains the user draft');
  assert(h.select('#settings-error').textContent.includes('Восстановлены предыдущие'));

  h.respond((url)=>{if(url.endsWith('/save'))throw new Error('connection lost');return {status:200,ok:true,data:{...baseline,apply:{status:'applied',revision:'someone-else'}}};});
  await h.run('saveSettings({preventDefault(){}})');
  assert.equal(h.run('settingsSaving'),false);
  assert.equal(h.run('settingsDirty'),true);
  assert.equal(h.select('#settings-save').disabled,true,'a lost save response must be reviewed, never resubmitted automatically');
  assert(h.select('#settings-error').textContent.includes('ответ на сохранение был потерян'));

  h.respond({status:200,ok:true,data:baseline});await h.run('loadSettings(true)');
  let finishLoad;
  h.respond(()=>new Promise((resolve)=>{finishLoad=resolve;}));
  const loading=h.run('loadSettings(true)');
  h.run("acceptSession({permissions:['vpn.view']})");
  finishLoad({status:200,ok:true,data:baseline});
  await loading;
  assert.equal(h.run('settingsSnapshot'),null,'revocation clears editor state');
  assert.equal(h.select('#settings-fields').innerHTML,'','revoked in-flight response cannot repopulate the form');
  assert.equal(h.run('settingsLoading'),false);
  const denied=h.requests.length;
  await h.run('loadSettings();validateSettings();saveSettings({preventDefault(){}})');
  assert.equal(h.requests.length,denied,'revoked settings endpoints are never fetched');
  h.run("authenticated=true;permissions=new Set(['users.manage'])");
  h.respond({status:200,ok:true,data:baseline});await h.run('loadSettings()');
  assert(h.run('settingsSnapshot'),'existing user administrators retain access');
  h.select('#setting-benchmark-full_interval').value='30m';h.run('settingsChanged()');
  h.respond((url)=>({status:200,ok:true,data:url.endsWith('/save')?{accepted:true,changed:true,revision:'timeout-candidate'}:{...baseline,apply:{status:'applying',revision:'timeout-candidate'}}}));
  await h.run('saveSettings({preventDefault(){}})');
  h.run('settingsApplyAttempts=59');
  await h.run('pollSettingsApply(settingsGeneration)');
  assert.equal(h.run('settingsSaving'),false,'bounded reconnect exhaustion clears the spinner');
  assert.equal(h.select('#settings-save').disabled,true,'timeout requires reviewing active values before another save');
  h.respond({status:200,ok:true,data:baseline});await h.run('loadSettings(true)');
  h.select('#setting-benchmark-full_interval').value='45m';h.run('settingsChanged()');
  let finishRevokedSave;
  h.respond(()=>new Promise((resolve)=>{finishRevokedSave=resolve;}));
  const revokedSave=h.run('saveSettings({preventDefault(){}})');
  h.run("acceptSession({permissions:['vpn.view']})");
  const revokedRequests=h.requests.length;
  finishRevokedSave({status:202,ok:true,data:{accepted:true,changed:true,revision:'revoked'}});
  await revokedSave;
  assert.equal(h.requests.length,revokedRequests,'a revoked save response cannot start apply polling');
  assert.equal(h.run('settingsSaving'),false);
  assert.equal(h.select('#settings-fields').innerHTML,'');
  h.run('showLogin()');
  assert.equal(h.run('settingsSnapshot'),null);
  assert.equal(h.select('#settings-fields').innerHTML,'','logout clears configuration and pending apply');

  h.run("authenticated=true;permissions=new Set(['config.manage'])");
  h.respond({status:503,ok:false,data:{error:'temporarily unavailable'}});
  await h.run('loadSettings()');
  assert.equal(h.run('settingsLoading'),false,'failed loading releases busy state');
  assert.equal(h.select('#settings-refresh').disabled,false,'failed loading can be retried');
  h.respond({status:200,ok:true,data:baseline});await h.run('loadSettings()');
  h.select('#setting-benchmark-full_interval').value='10m';h.run('settingsChanged()');
  h.respond({status:422,ok:false,data:{code:'settings_invalid',error:'invalid'}});
  await h.run('saveSettings({preventDefault(){}})');
  assert.equal(h.run('settingsSaving'),false,'invalid response clears saving state');
  assert.equal(h.select('#setting-benchmark-full_interval').value,'10m');
  assert(h.select('#settings-error').textContent.includes('Параметры не прошли проверку'));
}

async function panelEditorChecks() {
  const h=appHarness();await new Promise(setImmediate);
  h.run("authenticated=true;permissions=new Set(['config.manage']);csrf='panel-csrf'");
  const idle={supported:true,hostname:'alice.jopa',port:443,listen_ip:'192.168.1.1',url:'https://alice.jopa',status:'idle',certificate_changed:false,dns_automatic:true};
  const pem='-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n';
  const candidate={...idle,hostname:'alice.home.arpa',port:9444,url:'https://alice.home.arpa:9444',revision:'panel-candidate',status:'prepared',certificate_changed:true,certificate_pem:pem};
  const trial={...candidate,status:'awaiting_confirmation',confirmation_deadline:new Date(Date.now()+300000).toISOString()};
  h.respond({status:200,ok:true,data:{supported:false,status:'idle',error:'stable launcher upgrade required'}});
  await h.run('loadPanelStatus()');
  assert.equal(h.select('#panel-form').classList.contains('hidden'),true);
  assert(h.select('#panel-status').textContent.includes('upgrade required'),'unsupported capability has a visible reason');
  h.respond({status:200,ok:true,data:idle});await h.run('loadPanelStatus(true)');
  assert.equal(h.select('#panel-hostname').value,'alice.jopa');
  assert.equal(h.select('#panel-port').value,'443');
  assert(h.select('#panel-old-address').textContent.includes('https://alice.jopa'));
  assert(h.select('#panel-dns').textContent.includes('автоматически'));
  assert.equal(h.select('#panel-prepare').disabled,false);
  h.select('#panel-hostname').value='alice.home.arpa';h.select('#panel-port').value='9444';h.select('#panel-hostname').input();
  assert.equal(h.run('panelDirty'),true);
  await h.run('loadPanelStatus()');
  assert.equal(h.select('#panel-hostname').value,'alice.home.arpa','status polling retains typed address');
  let finishPrepare;
  h.respond(()=>new Promise((resolve)=>{finishPrepare=resolve;}));
  const preparing=h.run('preparePanel({preventDefault(){}})');
  assert.equal(h.select('#panel-prepare').disabled,true);
  const before=h.requests.length;await h.run('preparePanel({preventDefault(){}});applyPanel()');
  assert.equal(h.requests.length,before,'address mutations cannot overlap');
  finishPrepare({status:200,ok:true,data:candidate});await preparing;await new Promise(setImmediate);
  let request=h.requests.filter((item)=>item.url.endsWith('/prepare')).at(-1);
  assert.deepEqual(JSON.parse(request.options.body),{hostname:'alice.home.arpa',port:9444});
  assert.equal(request.options.headers['X-KRM-CSRF'],'panel-csrf');
  assert.equal(h.select('#panel-open').href,'https://alice.home.arpa:9444/#settings');
  assert.equal(h.select('#panel-certificate-download').download,'alice.home.arpa.crt');
  assert.equal(h.blobURLs.size,1,'prepared public certificate can be downloaded as a Blob');
  assert(!h.select('#panel-preview').innerHTML.includes(pem),'certificate bytes never enter HTML');
  assert.equal(h.select('#panel-apply').disabled,false);
  h.run('settingsSaving=true;updateSettingsControls()');
  const applyingBlocked=h.requests.length;await h.run('applyPanel()');
  assert.equal(h.requests.length,applyingBlocked,'core apply blocks address apply');
  h.run('settingsSaving=false;settingsDirty=true;updateSettingsControls()');
  h.select('#setting-benchmark-full_interval').value='17m';
  h.respond((url)=>({status:200,ok:true,data:url.endsWith('/apply')?{accepted:true}:trial}));
  await h.run('applyPanel()');
  request=h.requests.filter((item)=>item.url.endsWith('/apply')).at(-1);
  assert.deepEqual(JSON.parse(request.options.body),{revision:'panel-candidate'});
  assert.equal(h.select('#panel-confirm').classList.contains('hidden'),true,'confirmation is not offered before readiness');
  await h.run('pollPanelTrial(panelGeneration)');
  assert.equal(h.select('#setting-benchmark-full_interval').value,'17m','address status cannot overwrite a core draft');
  assert.equal(h.select('#panel-confirm').classList.contains('hidden'),false);
  assert.equal(h.select('#panel-confirm').disabled,true,'confirming cannot silently discard a core draft');
  assert(h.select('#panel-confirm-hint').textContent.includes('несохранённые'));
  const confirmingBlocked=h.requests.length;await h.run('confirmPanel()');
  assert.equal(h.requests.length,confirmingBlocked);
  h.run('settingsDirty=false;updateSettingsControls()');
  h.respond({status:202,ok:true,data:{accepted:true}});await h.run('confirmPanel()');
  assert.deepEqual(h.assignments,['https://alice.home.arpa:9444/#settings'],'explicit accepted confirmation navigates to verified candidate');
  request=h.requests.filter((item)=>item.url.endsWith('/confirm')).at(-1);
  assert.deepEqual(JSON.parse(request.options.body),{revision:'panel-candidate'});
  let prevented=false;h.handlers.beforeunload({preventDefault(){prevented=true;}});
  assert.equal(prevented,false,'authorized confirmation navigation does not warn about its own trial');

  h.run('clearPanelEditor()');assert.equal(h.blobURLs.size,0,'clearing revokes downloadable certificate');
  h.respond({status:200,ok:true,data:{...idle,hostname:'192.168.1.1',url:'https://192.168.1.1'}});
  await h.run('loadPanelStatus()');
  assert.equal(h.select('#panel-hostname').value,'192.168.1.1','an existing IP-only panel can prepare its first DNS name');
  h.select('#panel-hostname').value='alice.home.arpa';h.select('#panel-port').value='443';
  const noPort={...candidate,port:443,url:'https://alice.home.arpa',revision:'no-port'};
  h.respond({status:200,ok:true,data:noPort});await h.run('preparePanel({preventDefault(){}})');
  assert.equal(h.select('#panel-open').href,'https://alice.home.arpa/#settings','443 is omitted in the generated URL');
  h.respond({status:200,ok:true,data:{...noPort,dns_automatic:false}});await h.run('loadPanelStatus()');
  assert(h.select('#panel-dns').textContent.includes('Перед применением настройте'));

  // Server-supplied links must match the prepared hostname and port; arbitrary HTML/URLs are rejected.
  for(const malformed of [
    {...noPort,url:'http://alice.home.arpa'}, {...noPort,url:'https://attacker.example/'},
    {...noPort,url:'https://alice.home.arpa:9999'}, {...noPort,url:'https://user:secret@alice.home.arpa'},
    {...noPort,url:'https://alice.home.arpa/path'}, {...noPort,url:'javascript:alert(1)'},
    {...noPort,hostname:'<img src=x onerror=alert(1)>',url:'https://attacker.example'},
    {...noPort,certificate_pem:'<script>alert(1)</script>'}, {...noPort,port:'443'}, {...noPort,listen_ip:'0.0.0.bad'},
  ]){
    h.context.malformed=malformed;
    assert.throws(()=>h.run('validatedPanelStatus(malformed)'));
  }
  h.context.ipv6={...idle,hostname:'::1',listen_ip:'::1',url:'https://[::1]'};
  assert.equal(h.run('validatedPanelStatus(ipv6).url'),'https://[::1]','IPv6 baseline remains usable with brackets');
  h.run('panelState=validatedPanelStatus(ipv6)');
  h.select('#panel-hostname').value='::1';h.select('#panel-port').value='9444';
  assert.equal(h.run('panelInput().hostname'),'::1','port-only change can retain the fixed IPv6 IP');
  h.select('#panel-hostname').value='192.168.1.2';
  assert.throws(()=>h.run('panelInput()'),/Другой IP недоступен/);
  h.context.originalCandidate=noPort;h.run('renderPanelStatus(validatedPanelStatus(originalCandidate))');
  h.select('#panel-hostname').value=noPort.hostname;h.select('#panel-port').value='443';
  const safeURL=h.select('#panel-open').href;
  h.respond({status:200,ok:true,data:{...noPort,url:'https://attacker.example'}});await h.run('loadPanelStatus()');
  assert.equal(h.select('#panel-open').href,safeURL,'malformed responses cannot replace links');
  assert(h.select('#panel-error').textContent.includes('не соответствует'));

  h.run('clearPanelEditor()');h.respond({status:200,ok:true,data:idle});await h.run('loadPanelStatus()');
  h.select('#panel-hostname').value='alice.home.arpa';h.select('#panel-port').value='9444';
  h.respond({status:409,ok:false,data:{error:'panel preparation conflict'}});await h.run('preparePanel({preventDefault(){}})');
  assert(h.select('#panel-error').textContent.includes('conflict'));
  assert.equal(h.select('#panel-prepare').disabled,true,'preparation conflict requires status refresh');
  h.respond({status:200,ok:true,data:candidate});await h.run('loadPanelStatus(true)');
  h.respond((url)=>{if(url.endsWith('/apply'))throw new Error('connection lost');return {status:200,ok:true,data:trial};});
  await h.run('applyPanel()');
  const lostCount=h.requests.filter((item)=>item.url.endsWith('/apply')).length;
  await h.run('pollPanelTrial(panelGeneration)');
  assert.equal(h.requests.filter((item)=>item.url.endsWith('/apply')).length,lostCount,'lost apply response is never resubmitted automatically');
  assert.equal(h.select('#panel-confirm').classList.contains('hidden'),false);
  assert.equal(h.assignments.length,1,'trial readiness never automatically confirms or navigates');
  h.respond({status:200,ok:true,data:{...idle,revision:'panel-candidate',status:'rolled_back'}});
  await h.run('pollPanelTrial(panelGeneration)');
  assert.equal(h.run('panelTrialActive()'),false);
  assert(h.select('#panel-status').textContent.includes('восстановлен'));
  assert.equal(h.select('#panel-hostname').value,'alice.jopa');
  assert.equal(h.blobURLs.size,0,'rollback removes the unused certificate download');

  h.select('#panel-hostname').value='alice.home.arpa';h.select('#panel-port').value='9444';
  h.respond({status:200,ok:true,data:candidate});await h.run('preparePanel({preventDefault(){}})');
  let finishRevoked;
  h.respond(()=>new Promise((resolve)=>{finishRevoked=resolve;}));
  const revoked=h.run('applyPanel()');
  h.run("acceptSession({permissions:['vpn.view']})");
  const revokedCount=h.requests.length;finishRevoked({status:202,ok:true,data:{accepted:true}});await revoked;
  assert.equal(h.requests.length,revokedCount,'revoked apply response cannot start polling');
  assert.equal(h.run('panelState'),null);
  assert.equal(h.blobURLs.size,0,'permission revocation revokes certificate blobs');
  assert.equal(h.select('#panel-hostname').value,'');
  assert.equal(h.select('#panel-open').href,undefined);
  await h.run('loadPanelStatus();preparePanel({preventDefault(){}});applyPanel();confirmPanel()');
  assert.equal(h.requests.length,revokedCount,'revoked address requests are not fetched');

  h.run("authenticated=true;permissions=new Set(['users.manage'])");
  h.respond({status:200,ok:true,data:candidate});await h.run('loadPanelStatus()');
  h.respond({status:202,ok:true,data:{accepted:true}});await h.run('applyPanel()');
  h.respond({status:200,ok:true,data:{...candidate,status:'applying'}});
  h.run('panelDeadline=Date.now()-1');await h.run('pollPanelTrial(panelGeneration)');
  assert.equal(h.run('panelTrialActive()'),false,'UI timeout releases the trial busy state without claiming success');
  assert.equal(h.select('#panel-prepare').disabled,true,'unknown address outcome requires a status refresh');
  assert(h.select('#panel-error').textContent.includes('через 5 минут'));
  h.run('clearPanelEditor()');
  h.respond({status:200,ok:true,data:trial});await h.run('loadPanelStatus()');
  const oldAssignments=h.assignments.length;
  h.respond((url)=>{if(url.endsWith('/confirm'))throw new Error('confirmation connection lost');return {status:200,ok:true,data:trial};});
  await h.run('confirmPanel()');
  const confirmations=h.requests.filter((item)=>item.url.endsWith('/confirm')).length;
  await h.run('pollPanelTrial(panelGeneration)');
  assert.equal(h.requests.filter((item)=>item.url.endsWith('/confirm')).length,confirmations,'lost confirmation response is never blindly retried');
  assert.equal(h.assignments.length,oldAssignments,'uncertain confirmation does not claim success or navigate');
  assert(h.select('#panel-error').textContent.includes('подтверждение потерян'));
  h.run('showLogin()');
  assert.equal(h.run('panelState'),null);
  assert.equal(h.select('#panel-form').classList.contains('hidden'),true);
  assert.equal(h.blobURLs.size,0);
}

async function workspaceRegressionChecks() {
  const h = appHarness(); await new Promise(setImmediate);
  h.run("closeDialog('login'); authenticated=true; permissions=new Set(allPermissions); activePage='devices'; statusData={capabilities:{client_policy:true,wake_on_lan:true}}; renderClients([{name:'Desk',mac:'02:00:00:00:00:01',ip:'192.168.1.5'}])");
  const original = h.select('#clients-body').innerHTML;
  const policy = h.select('.policy'); policy.parentElement = h.select('#clients-body'); policy.focus();
  h.run("renderClients([{name:'Desk updated',mac:'02:00:00:00:00:01',ip:'192.168.1.6'}])");
  assert.equal(h.select('#clients-body').innerHTML, original, '3s polling must preserve a focused native policy select');
  h.run("acceptSession({permissions:['router.clients']})");
  assert(!h.select('#clients-body').innerHTML.includes('class="policy"'), 'permission revocation bypasses focused-control preservation immediately');

  h.run("permissions=new Set(allPermissions); nodesData=[{id:'null',label:'Missing',sources:['b'],measurement:{latency_ms:null,speed_mbps:null,score:null}},{id:'zero',label:'Zero',sources:['a'],active:true,in_pool:true,measurement:{latency_ms:0,speed_mbps:0,score:0,healthy:true,checked_at:'2026-10-09T00:00:00Z'}},{id:'known',label:'Known',sources:['b'],measurement:{latency_ms:20,speed_mbps:50,score:10,healthy:false,checked_at:'2026-10-09T00:00:00Z'}}]");
  h.select('#node-sort').value='latency';
  assert.deepEqual(Array.from(h.run('filteredNodes().map(n=>n.id)')), ['zero','known','null'], 'zero latency is valid and null sorts last');
  h.select('#node-sort').value='speed';
  assert.deepEqual(Array.from(h.run('filteredNodes().map(n=>n.id)')), ['known','zero','null'], 'zero speed is valid and missing values sort last');
  h.select('#node-source-filter').value='a';
  assert.deepEqual(Array.from(h.run('filteredNodes().map(n=>n.id)')), ['zero']);
  h.select('#node-source-filter').value=''; h.select('#node-status-filter').value='error';
  assert.deepEqual(Array.from(h.run('filteredNodes().map(n=>n.id)')), ['known']);
  h.select('#node-status-filter').value='unchecked'; assert.deepEqual(Array.from(h.run('filteredNodes().map(n=>n.id)')), ['null']);
  h.select('#node-status-filter').value='active'; assert.deepEqual(Array.from(h.run('filteredNodes().map(n=>n.id)')), ['zero']);
  h.select('#node-status-filter').value=''; h.run('renderNodes()');
  assert(h.select('#nodes-body').innerHTML.includes('node-details'), 'mobile retains source/type/score disclosure');
  assert(h.select('#nodes-body').innerHTML.includes('0 мс'), 'measured zero latency must not become unavailable');

  h.run('initializeWorkspace(); activePage="devices"'); h.document.hidden=true;
  const before=h.requests.length; await h.run('poll()'); assert.equal(h.requests.length,before,'hidden pages pause ordinary polling');
  h.respond((url)=>({status:200,ok:true,data:url.endsWith('/session')?{csrf:'refreshed',permissions:['router.clients']} : url.endsWith('/clients')?{value:[],updated_at:new Date().toISOString()}:{}}));
  h.document.hidden=false; h.handlers['document:visibilitychange'](); await new Promise(setImmediate);
  assert(h.requests.slice(before).some((item)=>item.url.endsWith('/session')),'visibility resume refreshes session before workspace');
}

async function foregroundRefreshChecks() {
  const h=appHarness();await new Promise(setImmediate);
  h.run("closeDialog('login');authenticated=true;permissions=new Set(allPermissions);csrf='fixture-csrf'");
  const source={id:'primary',name:'Primary',url:'https://subscription.example.invalid',enabled:true,headers:{}};
  let sources=[source];let users=[{id:'fixture-user',username:'operator',enabled:true,permissions:['vpn.view'],updated_at:'fixture-original'}];
  let activeSlot=0;
  h.respond((url,options)=>{
    const body=options.body?JSON.parse(options.body):{};
    if(url.endsWith('/subscriptions/save'))sources=[body];
    if(url.endsWith('/subscriptions/delete'))sources=[];
    if(url.endsWith('/users/save'))users=[{...body,updated_at:'fixture-updated'}];
    if(url.endsWith('/users/delete'))users=[];
    const data=url.endsWith('/subscriptions')?{sources}:url.endsWith('/users')?{users,permissions:h.allPermissions}:url.endsWith('/session')?{csrf:'fixture-csrf',permissions:h.allPermissions}:url.endsWith('/status')?{version:'1.3.1-fixture',state:{active_slot:activeSlot,pool:[{index:0,node_id:'first',label:'First'},{index:1,node_id:'second',label:'Second'}],sources:{}}}:{};
    return {status:200,ok:true,data};
  });
  await h.run('loadSubscriptions()');
  const subscriptionButton=h.select('.subscription-edit');subscriptionButton.classList.add('subscription-edit');subscriptionButton.dataset.id='primary';subscriptionButton.parentElement=h.select('#subscriptions-body');h.many.set('.subscription-edit',[subscriptionButton]);subscriptionButton.focus();
  const initialSources=h.select('#subscriptions-body').innerHTML;
  await h.run('loadSubscriptions()');assert.equal(h.select('#subscriptions-body').innerHTML,initialSources,'unchanged subscription polling preserves focused row');
  sources=[{...source,name:'Background name'}];await h.run('loadSubscriptions()');assert(h.select('#subscriptions-body').innerHTML.includes('Background name'),'late source result refreshes focused action row');assert.equal(h.document.activeElement,subscriptionButton,'late source result restores action focus');
  h.run("openSubscriptionEditor('primary')");h.select('#subscription-name').value='Foreground name';
  await h.run('saveSubscription({preventDefault(){}})');
  assert(h.select('#subscriptions-body').innerHTML.includes('Foreground name'),'subscription save refreshes row while opener focused');
  subscriptionButton.focus();await h.run("deleteSubscription('primary')");
  assert(h.select('#subscriptions-body').innerHTML.includes('Подписки не настроены'),'focused subscription delete removes row');

  await h.run('loadUsers()');
  const userButton=h.select('.user-edit');userButton.classList.add('user-edit');userButton.dataset.id='fixture-user';userButton.parentElement=h.select('#users-body');h.many.set('.user-edit',[userButton]);userButton.focus();
  const initialUsers=h.select('#users-body').innerHTML;await h.run('loadUsers()');assert.equal(h.select('#users-body').innerHTML,initialUsers,'unchanged users preserve focused row');users=[{...users[0],username:'Background user'}];await h.run('loadUsers()');assert(h.select('#users-body').innerHTML.includes('Background user'),'late user result refreshes focused action row');assert.equal(h.document.activeElement,userButton,'late user result restores focus');
  h.run("openUserEditor('fixture-user')");h.select('#user-name').value='Foreground user';await h.run('saveUser({preventDefault(){}})');
  assert(h.select('#users-body').innerHTML.includes('Foreground user'),'user save refreshes row while opener focused');
  const toggle=h.select('.user-toggle');toggle.classList.add('user-toggle');toggle.dataset.id='fixture-user';toggle.parentElement=h.select('#users-body');h.many.set('.user-toggle',[toggle]);toggle.focus();await h.run("toggleUser('fixture-user')");
  assert(h.select('#users-body').innerHTML.includes('Заблокирован'),'focused toggle refreshes enabled badge');
  toggle.focus();await h.run("deleteUser('fixture-user')");assert(h.select('#users-body').innerHTML.includes('Нет пользователей'),'focused user delete removes row');

  await h.run('loadStatus()');const slot=h.select('.slot-switch');slot.classList.add('slot-switch');slot.dataset.index='1';slot.parentElement=h.select('#pool');h.many.set('.slot-switch',[slot]);slot.focus();
  const initialPool=h.select('#pool').innerHTML;await h.run('loadStatus()');assert.equal(h.select('#pool').innerHTML,initialPool,'unchanged pool preserves focused action');activeSlot=1;await h.run('loadStatus()');assert(h.select('#pool').innerHTML.indexOf('pool-item active')>h.select('#pool').innerHTML.indexOf('First'),'late accepted switch updates active badge despite focused action');activeSlot=0;await h.run('loadStatus(true)');activeSlot=1;await h.run('loadStatus(true)');assert(h.select('#pool').innerHTML.indexOf('pool-item active')>h.select('#pool').innerHTML.indexOf('First'),'foreground pool refresh moves active badge');
  assert.equal(h.document.activeElement,slot,'foreground refresh restores same slot action focus');
}

async function dialogRegressionChecks() {
  const h=appHarness(); await new Promise(setImmediate);
  h.run("closeDialog('login'); authenticated=true; initializeDialogs()");
  const opener=h.select('#add-subscription'), first=h.select('#subscription-name'), last=h.select('#subscription-cancel');
  first.parentElement=h.select('#subscription-modal'); last.parentElement=h.select('#subscription-modal');
  h.many.set('subscription-modal button, input, select, textarea, a[href], [tabindex]',[first,last]);
  opener.focus(); h.run("openDialog('subscription-modal','#subscription-name')");
  assert.equal(h.document.activeElement,first); assert.equal(h.select('main').inert,true);
  let prevented=false; last.focus(); h.handlers['document:keydown']({key:'Tab',shiftKey:false,preventDefault(){prevented=true;}});
  assert(prevented); assert.equal(h.document.activeElement,first,'last Tab wraps to first modal field');
  h.handlers['document:keydown']({key:'Tab',shiftKey:true,preventDefault(){}}); assert.equal(h.document.activeElement,last,'Shift+Tab wraps to last field');
  h.handlers['document:keydown']({key:'Escape',preventDefault(){}});
  assert(h.select('#subscription-modal').classList.contains('hidden')); assert.equal(h.select('main').inert,false); assert.equal(h.document.activeElement,opener,'closing returns keyboard focus');
  h.run("openDialog('login','#username',false)"); h.handlers['document:keydown']({key:'Escape',preventDefault(){}});
  assert(!h.select('#login').classList.contains('hidden'),'required login cannot be dismissed with Escape');
}

async function settingsAndPanelProgressChecks() {
  const h=appHarness(); await new Promise(setImmediate); h.run("authenticated=true; permissions=new Set(allPermissions)");
  const fields=h.run('settingsSections.flatMap(([,prefix,items])=>items.map(([key])=>prefix+"."+key))');
  assert.equal(fields.length,36,'basic/advanced must retain the complete editable allowlist');
  assert.equal(new Set(fields).size,36,'basic/advanced fields appear once in the submitted DTO');
  assert(source.includes('settings-advanced'),'advanced fields stay in a disclosure, not omitted from the form');
  assert(source.includes('data-settings-dependency="'),'disabled speed/cache dependencies remain persisted');
  h.context.trialStatus={supported:true,hostname:'alice.jopa',port:443,listen_ip:'192.168.1.1',url:'https://alice.jopa',status:'awaiting_confirmation',revision:'trial-only',confirmation_deadline:new Date(Date.now()+180000).toISOString(),certificate_changed:false};
  h.run('renderPanelStatus(trialStatus)');
  assert.equal(h.select('#panel-step-confirm')['aria-current'],'step');
  assert.match(h.select('#panel-countdown').textContent,/2:|3:/);
  h.context.trialStatus.confirmation_deadline=new Date(Date.now()-1000).toISOString(); h.run('renderPanelStatus(trialStatus)');
  assert(h.select('#panel-countdown').textContent.includes('истёк'));
  assert(!h.requests.some((item)=>item.url.endsWith('/confirm')),'countdown expiration never confirms automatically');
}

function chartRegressionChecks() {
  const h=appHarness(); const end=Date.parse('2026-10-09T12:00:00Z');
  const sample=(seconds,value)=>({updated_at:new Date(end-seconds*1000).toISOString(),cpu_percent:value});
  h.context.chartEnd=end; h.context.chartSamples=[sample(1200,55),sample(100,10),sample(95,20),sample(90,null),sample(85,0),sample(40,30),sample(-10,99),{updated_at:'invalid',cpu_percent:42}];
  const html=h.run("chartHTML(chartSamples,[['cpu_percent','CPU','blue']],true,15,chartEnd)");
  assert.equal((html.match(/<polyline/g)||[]).length,1,'null readings break connecting lines');
  assert.equal((html.match(/<circle/g)||[]).length,2,'missing intervals remain gaps with isolated observations');
  assert(html.includes('за 15 минут')&&html.includes('tabindex="0"'),'chart exposes range and keyboard access');
  assert.equal(h.run('chartRows(chartSamples,15,chartEnd).length'),5,'15m excludes old/future/invalid samples');
  assert.equal(h.run('chartRows(chartSamples,60,chartEnd).length'),6,'60m retains older observations');
  assert(h.run("chartHTML([{updated_at:new Date(chartEnd).toISOString(),cpu_percent:null}],[['cpu_percent','CPU','blue']],true,15,chartEnd)").includes('недоступны'));
  const element=h.select('#usage-chart'), svg=h.select('#chart-svg'), tooltip=h.select('#chart-tooltip'), cursor=h.select('#chart-cursor');
  element.querySelector=(selector)=>({'svg':svg,'.chart-tooltip':tooltip,'.chart-cursor':cursor}[selector]);
  svg.getBoundingClientRect=()=>({left:0,width:600});
  h.context.fixtureChart=element;h.run("chartRange=15;bindChartInspector(fixtureChart,chartSamples,[['cpu_percent','CPU','blue']],true,chartEnd)");
  svg.onfocus();assert(tooltip.textContent.includes('30.0 %'));
  svg.onkeydown({key:'Home',preventDefault(){}});assert(tooltip.textContent.includes('10.0 %'),'Home selects first visible observation');
  svg.onpointermove({pointerType:'mouse',clientX:42+(900-60)/900*530});
  assert.equal(tooltip.textContent,'Нет наблюдения в этой точке','tooltips must not interpolate missing measurements');
  svg.onkeydown({key:'Escape'});assert(tooltip.classList.contains('hidden'));
}

async function main() {
  chartRegressionChecks();
  await workspaceRegressionChecks();
  await foregroundRefreshChecks();
  await dialogRegressionChecks();
  await settingsAndPanelProgressChecks();
  await panelEditorChecks();
  await settingsEditorChecks();
  await updateChannelChecks();
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
