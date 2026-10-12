'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const read = p => fs.readFileSync(path.join(root,p),'utf8');
const html=read('internal/web/ui/static/index.html');
const app=read('internal/web/ui/static/app.js');
const components=read('internal/web/ui/static/components.js');

assert.match(html,/data-tab="components"[^>]*data-permission="updates.manage"/);
assert.match(html,/id="tab-components"/);
assert.match(html,/id="component-check"/);
assert.match(html,/id="components-list"/);
assert.match(html,/src="\/assets\/components.js"/);
assert.match(app,/components:\s*'updates.manage'/);
assert.match(components,/\/api\/v1\/components\/status/);
assert.match(components,/\/api\/v1\/components\/check/);
assert.match(components,/\/api\/v1\/components\/install/);
assert.match(components,/addEventListener/);
console.log('Component manager UI contract passed');

const vm = require('node:vm');
const lookup = new Map();
function node(tag) {
  return {
    tag, disabled: false, textContent: '', children: [],
    setAttribute() {}, addEventListener() {},
    replaceChildren() { this.children = []; },
    append(...items) { this.children.push(...items); },
    querySelectorAll() { return this.children; },
  };
}
function select(id) {
  if (!lookup.has(id)) lookup.set(id, node('div'));
  return lookup.get(id);
}
const calls = [];
const context = vm.createContext({
  Date,
  $: select,
  document: { createElement: node },
  window: { confirm: () => false },
  authenticated: true,
  can: () => true,
  toast: error => { throw new Error(error); },
  api: async (path, options = {}) => {
    calls.push({ path, method: options?.method || 'GET' });
    return {
      checked_at: '2026-10-12T00:00:00Z',
      items: [{ id: 'xray', installed_version: '1.0.0', latest_version: '1.1.0', missing: false, update_available: true }],
    };
  },
});
vm.runInContext(components, context);
(async () => {
  await vm.runInContext('loadComponents(false)', context);
  const update = select('#components-details').children.find(element => element.textContent === 'Обновить');
  assert(update && !update.disabled, 'update action should be usable after status request completes');
  await vm.runInContext('loadComponents(true)', context);
  assert.deepEqual(calls.map(call => [call.path, call.method]), [
    ['/api/v1/components/status', 'GET'],
    ['/api/v1/components/check', 'POST'],
  ]);
  console.log('Component manager manual check and button readiness passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
