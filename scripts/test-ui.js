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
  const context = vm.createContext({ $: select, renderPool() {}, renderSources() {}, fmtAge: () => '—' });
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
  const select = (selector) => {
    if (!elements.has(selector)) {
      const classes = new Set(['hidden']);
      elements.set(selector, {
        value: '', textContent: '', innerHTML: '', style: {}, dataset: {}, disabled: false,
        classList: {
          add: (name) => classes.add(name), remove: (name) => classes.delete(name), contains: (name) => classes.has(name),
          toggle: (name, force) => { if (force) classes.add(name); else classes.delete(name); },
        },
        addEventListener(name, fn) { this[name] = fn; }, focus() {},
      });
    }
    return elements.get(selector);
  };
  let response = { status: 401, ok: false, data: { error: 'unauthorized' } };
  let confirmResult = true;
  const context = vm.createContext({
    document: { querySelector: select, querySelectorAll: () => [] }, navigator: {}, Date,
    window: { location: { reload: () => { reloads++; } } },
    setTimeout() {}, clearInterval: (timer) => intervals.delete(timer),
    setInterval: (fn) => { intervals.add(fn); return fn; }, confirm: () => confirmResult,
    fetch: async (url, options) => {
      requests.push({ url, options });
      const current = typeof response === 'function' ? await response(url, options) : response;
      return { ...current, headers: { get: () => 'application/json' }, json: async () => current.data };
    },
  });
  vm.runInContext(source, context);
  return { context, select, requests, intervals, reloads: () => reloads, confirm: (next) => { confirmResult = next; }, respond: (next) => { response = next; }, run: (code) => vm.runInContext(code, context) };
}

async function versionReloadChecks() {
  for (const interruption of ['none', 'expired', 'logout']) {
    const h = appHarness();
    await new Promise(setImmediate);
    h.run('authenticated = true');
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
        ? { csrf: 'new-session' } : url.endsWith('/update/status') ? {} : status('1.2.0-rc.1') }));
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
  h.run("csrf = 'test-csrf'");
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

  const injection = '<img src=x onerror="alert(1)">';
  h.context.injection = injection;
  h.run(`renderPool({ pool: [{ index: 0, label: injection, node_id: 'node', score: 1 }], active_slot: 0 });
    renderSources({ provider: { name: injection, status: 'healthy', last_error: injection, node_count: 1 } });
    nodesData = [{ label: injection, sources: [injection], network: 'ws', security: 'tls', measurement: {} }]; renderNodes();
    statusData = { capabilities: { wake_on_lan: true, client_policy: true } };
    renderClients([{ name: injection, mac: injection, ip: injection, connection_policy: injection }]);
    renderMetrics({ ports: [{ id: injection, link: injection, speed: injection }] });`);
  for (const selector of ['#pool', '#sources', '#nodes-body', '#clients-body', '#ports']) {
    const html = h.select(selector).innerHTML;
    assert(!html.includes('<img'), `${selector} must escape provider/router text`);
    assert(html.includes('&lt;img'), `${selector} must preserve escaped text`);
  }
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
  h.run("authenticated = true; csrf = 'update-csrf'");
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
  h.run("authenticated = true; csrf = 'update-csrf'");
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
  h.run("authenticated = true; csrf = 'update-csrf'");
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

async function main() {
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

  console.log('UI authentication, rendering, updates, service worker, status and event pagination checks passed.');
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
