const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
function harness() {
  const elements = {};
  const timers = new Map();
  let nextTimer = 0;
  const h = { stored: 'old', fail: false, writes: 0, confirmed: true, notices: [] };
  const $ = id => elements[id] ||= {
    value: '', textContent: '', disabled: false, listeners: {},
    classList: { values: new Set(), add(v) { this.values.add(v); }, remove(v) { this.values.delete(v); } },
    focus() {}, setSelectionRange() {}, showModal() {}, close() { this.listeners.close?.(); },
    addEventListener(name, fn) { this.listeners[name] = fn; }
  };
  const context = vm.createContext({ $, translate: s => s, toast: s => h.notices.push(s),
    confirmAction: async () => h.confirmed, closeProviderUsagePopover() {}, requestAnimationFrame: fn => fn(),
    localStorage: { getItem: () => h.stored, setItem(key, value) { h.writes++; if (h.fail) throw Error('quota'); h.stored = value; } },
    setTimeout(fn) { timers.set(++nextTimer, fn); return nextTimer; }, clearTimeout(id) { timers.delete(id); }
  });
  const functions = source.slice(source.indexOf('function readMemo()'), source.indexOf('function renderProviderOptions()'));
  const events = source.slice(source.indexOf('$("memoButton").addEventListener'), source.indexOf('$("providerUsageButton").addEventListener'));
  assert.ok(functions && events, 'memo source sections found');
  vm.runInContext('let memoSaveTimer = 0; let memoNeedsSave = false; const memoStorageKey = "memo"; const memoSaveDelayMilliseconds = 250;\n' + functions + events, context);
  h.run = code => vm.runInContext(code, context);
  h.$ = $;
  h.flush = () => { for (const [id, fn] of [...timers]) { timers.delete(id); fn(); } };
  h.input = text => { $('memoText').value = text; $('memoText').listeners.input(); };
  return h;
}
test('debounced save succeeds and clears pending state', () => {
  const h = harness(); h.input('one'); h.input('two'); assert.equal(h.writes, 0);
  h.flush(); assert.equal(h.writes, 1); assert.equal(h.stored, 'two'); assert.equal(h.run('memoNeedsSave'), false);
});
test('failed autosave is retried on close even after timer cleared', () => {
  const h = harness(); h.fail = true; h.input('draft'); h.flush();
  assert.equal(h.run('memoSaveTimer'), 0); assert.equal(h.run('memoNeedsSave'), true);
  assert.equal(h.$('memoSaveState').classList.values.has('error'), true);
  h.fail = false; h.run('closeMemo()'); assert.equal(h.stored, 'draft'); assert.equal(h.writes, 2);
});
test('dialog close event retries pending save', () => {
  const h = harness(); h.fail = true; h.input('draft'); h.flush(); h.fail = false;
  h.$('memoDialog').listeners.close(); assert.equal(h.stored, 'draft');
});
test('continued failure preserves draft and error on reopening', () => {
  const h = harness(); h.fail = true; h.input('draft'); h.flush(); h.run('closeMemo(); openMemo()');
  assert.equal(h.$('memoText').value, 'draft'); assert.equal(h.run('memoNeedsSave'), true);
  assert.equal(h.$('memoSaveState').classList.values.has('error'), true); assert.equal(h.stored, 'old');
});
test('successful clear persists empty content and notifies', async () => {
  const h = harness(); h.run('openMemo()'); await h.run('clearMemo()');
  assert.equal(h.stored, ''); assert.deepEqual(h.notices, ['記事已清除']); assert.equal(h.run('memoNeedsSave'), false);
});
test('failed clear does not notify success and remains retryable', async () => {
  const h = harness(); h.run('openMemo()'); h.fail = true; await h.run('clearMemo()');
  assert.deepEqual(h.notices, []); assert.equal(h.stored, 'old'); assert.equal(h.run('memoNeedsSave'), true);
  h.run('openMemo()'); assert.equal(h.$('memoText').value, '');
  h.fail = false; h.run('closeMemo()'); assert.equal(h.stored, '');
});
test('cancel clear leaves stored and editor content intact', async () => {
  const h = harness(); h.run('openMemo()'); h.confirmed = false; await h.run('clearMemo()');
  assert.equal(h.$('memoText').value, 'old'); assert.equal(h.writes, 0); assert.deepEqual(h.notices, []);
});
test('keyboard save retains failure state then clears it on successful retry', () => {
  const h = harness(); h.input('draft'); h.fail = true;
  const event = { ctrlKey: true, key: 's', preventDefault() {} };
  h.$('memoText').listeners.keydown(event); assert.equal(h.run('memoNeedsSave'), true);
  h.fail = false; h.$('memoText').listeners.keydown(event); assert.equal(h.run('memoNeedsSave'), false);
  assert.equal(h.stored, 'draft'); assert.equal(h.$('memoSaveState').classList.values.has('error'), false);
});
