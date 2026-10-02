'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'web/app.js'), 'utf8');
assert.equal(source, fs.readFileSync(path.join(root, 'internal/web/ui/static/app.js'), 'utf8'));
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

async function main() {
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

  console.log('UI event pagination checks passed.');
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
