// 使用合成 File 與 DOM 驗證選檔／DLG 流程；不讀取真實帳號、不啟動上游連線。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const begin = source.indexOf('let providerAccountImportBusy = false;');
const end = source.indexOf('async function connectProviderOAuth()', begin);
assert.ok(begin >= 0 && end > begin);

function fixture() {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, {
      value: '', dataset: {}, children: [], selectedIndex: -1, open: false,
      replaceChildren() { this.children = []; this.selectedIndex = -1; },
      append(child) { this.children.push(child); },
      get selectedOptions() { return this.selectedIndex < 0 ? [] : [this.children[this.selectedIndex]]; },
      click() { this.clicked = true; },
      addEventListener(event, fn) { this[event] = fn; },
      showModal() { this.open = true; },
      dismiss(value) { this.returnValue = value; this.open = false; this.close(); },
    });
    return elements.get(id);
  };
  const calls = [], messages = [];
  const accounts = [{ id: 'a', email: 'same@example.invalid' }, { id: 'b', email: 'same@example.invalid' }, { id: 'c', problem: '不可匯入' }];
  const state = { selectedProviderSettingsID: 'codex', providerSettingsDraft: null, providerSettings: { providers: [{ id: 'codex', openai_codex_responses: {} }] }, providerModelLists: {}, providerModelErrors: {}, providerTestedModels: {} };
  element('providerSettingType').value = 'openai-codex-responses';
  const context = vm.createContext({
    $: element, state, document: { createElement: () => ({}) }, translate: s => s, toast: s => messages.push(s),
    request: async (url, options) => { calls.push({ url, options }); return accounts; },
    persistProviderSetting: async () => ({ id: state.selectedProviderSettingsID }),
    hideProviderResetRateLimit() {}, loadProviderOAuthStatus: async () => {}, loadProviderModels: async () => {},
    clearTimeout() {}, providerOAuthPollTimer: 0, activeProviderID: () => 'codex', renderProviderUsage() {},
  });
  vm.runInContext(source.slice(begin, end), context);
  function start() {
    context.selectProviderAccountFile();
    assert.equal(element('providerAccountFile').clicked, true);
    assert.equal(calls.length, 0, '先開啟原生選檔器，不能讀取預設檔案');
    element('providerAccountFile').files = [{ name: 'synthetic-accounts.json', size: 123 }];
    return context.importSelectedProviderAccountFile();
  }
  return { context, element, calls, messages, state, start };
}

test('選檔後開啟 DLG，不預選；選第二筆才匯入', async () => {
  const f = fixture();
  const work = f.start();
  await new Promise(setImmediate);
  assert.equal(f.element('providerAccountDialog').open, true);
  assert.equal(f.element('providerAccountList').selectedIndex, -1);
  assert.equal(f.element('confirmProviderAccount').disabled, true);
  assert.equal(f.element('providerAccountList').children[2].disabled, true);
  f.element('providerAccountList').selectedIndex = 1;
  f.element('providerAccountList').onchange();
  assert.equal(f.element('confirmProviderAccount').disabled, false);
  f.element('providerAccountDialog').dismiss('import');
  await work;
  assert.equal(f.calls.length, 2);
  assert.ok(f.calls[1].url.endsWith('/accounts/b'));
  assert.equal(f.calls[0].options.body, f.calls[1].options.body);
  assert.equal(f.element('providerAccountList').children.length, 0);
  assert.equal(f.element('providerAccountFile').value, '');
  assert.equal(f.element('selectProviderAccountFile').disabled, false);
  assert.deepEqual(f.messages, ['帳號已匯入']);
});

test('取消選檔或 DLG 不匯入', async () => {
  const f = fixture();
  f.context.selectProviderAccountFile();
  await f.context.importSelectedProviderAccountFile();
  assert.equal(f.calls.length, 0);
  const work = f.start();
  await new Promise(setImmediate);
  f.element('providerAccountList').selectedIndex = 1;
  f.element('providerAccountDialog').dismiss('cancel');
  await work;
  assert.equal(f.calls.length, 1);
});

test('Provider 切換、不可用項目與解析失敗都不匯入', async () => {
  for (const mode of ['switch', 'disabled', 'error']) {
    const f = fixture();
    if (mode === 'error') f.context.request = async () => { throw new Error('檔案格式錯誤'); };
    const work = f.start();
    await new Promise(setImmediate);
    if (mode !== 'error') {
      f.element('providerAccountList').selectedIndex = mode === 'disabled' ? 2 : 1;
      if (mode === 'switch') f.state.selectedProviderSettingsID = 'another';
      f.element('providerAccountDialog').dismiss('import');
    }
    await work;
    assert.ok(f.calls.length <= 1);
    assert.equal(f.element('selectProviderAccountFile').disabled, false);
    if (mode === 'error') assert.deepEqual(f.messages, ['檔案格式錯誤']);
  }
});

test('較舊的 OAuth 回應不能覆蓋新帳號狀態', async () => {
  const f = fixture();
  const pending = [], rendered = [];
  f.context.request = () => new Promise(resolve => pending.push(resolve));
  f.context.updateProviderOAuthStatusUI = status => rendered.push(status.account_email);
  f.context.loadProviderResetRateLimit = async () => {};
  const begin = source.indexOf('async function loadProviderOAuthStatus(');
  const end = source.indexOf('function pollProviderOAuth(', begin);
  vm.runInContext(source.slice(begin, end), f.context);
  const older = f.context.loadProviderOAuthStatus('codex');
  const newer = f.context.loadProviderOAuthStatus('codex');
  pending[1]({ status: 'connected', account_email: 'new@example.invalid' });
  await newer;
  pending[0]({ status: 'connected', account_email: 'old@example.invalid' });
  assert.equal(await older, null);
  assert.deepEqual(rendered, ['new@example.invalid']);
});
