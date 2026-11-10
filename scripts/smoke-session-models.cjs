// 合成表單驗證建立 Session 的選項、繼承、非同步切換及實際送出的欄位。
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const html = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/index.html'), 'utf8');
class Option { constructor(text, value) { this.text = text; this.value = value; } }
const elements = new Map();
const $ = id => {
  if (!elements.has(id)) elements.set(id, {
    value: '', checked: false, dataset: {}, options: [], textContent: '',
    replaceChildren(...options) { this.options = options; this.value = options[0]?.value || ''; },
    add(option) { this.options.push(option); }, focus() {}, select() {}, reset() {}, showModal() {}, close() {},
    classList: { toggle(key, value) { this[key] = value; } },
  });
  return elements.get(id);
};
const state = {
  agent: { id: 'agent' }, workspace: { id: 'workspace', default_provider_id: 'a', model: 'a2' },
  providers: [{ id: 'a', default_model: 'a1' }, { id: 'b', default_model: 'b1' }, { id: 'disabled', enabled: false }],
  providerModelLists: { a: ['a1', 'a2', 'a1'], b: ['b1'] }, providerModelErrors: {},
};
const pending = new Map();
let saved;
const ctx = vm.createContext({ $, Option, state,
  selectableProviders: () => state.providers.filter(p => p.enabled !== false),
  providerDisplayName: p => typeof p === 'string' ? p : p.id,
  renderProjectOptions() {}, renderPermissionOptions() {}, defaultSessionTitle: () => '測試',
  loadProviderModels: async id => { await new Promise(resolve => pending.set(id, resolve)); state.providerModelLists[id] = [id + '1']; },
  request: async (url, options) => { saved = { url, ...JSON.parse(options.body) }; return { id: 'created' }; },
  loadSessions: async () => {}, selectSession: async () => {}, toast: message => { throw new Error(message); },
});
for (const [start, end] of [
  ['function renderSessionProviderOptions()', 'function defaultModelForProvider('],
  ['async function initializeWorkspaceModels(', 'function sessionRuntimeModels('],
  ['function openSessionDialog(', 'async function createDefaultProjectSession('],
]) vm.runInContext(source.slice(source.indexOf(start), source.indexOf(end)), ctx);

(async () => {
  ctx.openSessionDialog('project');
  assert.deepEqual($('newProvider').options.map(o => o.value), ['', 'a', 'b']);
  assert.equal($('newProvider').value, 'a');
  assert.deepEqual($('newModelCatalog').options.map(o => o.value), ['', 'a1', 'a2', '__custom_model__']);
  assert.match($('newModelCatalog').options[0].text, /a2/);
  assert.equal($('newModel').value, '', '預設值交給後端繼承');
  $('newModelCatalog').value = 'a1'; ctx.selectModelChoice('session');
  await ctx.createSession({ preventDefault() {} });
  assert.equal(saved.provider_id, 'a'); assert.equal(saved.model, 'a1'); assert.equal(saved.project_id, 'project');
  $('newProvider').value = 'b'; await ctx.refreshModelChoices('session', true);
  assert.equal($('newModel').value, '', '切換 Provider 不可攜帶舊模型');
  assert.ok(!$('newModelCatalog').options.some(o => o.value === 'a1'));
  assert.match($('newModelCatalog').options[0].text, /b1/);
  $('newProvider').value = ''; await ctx.refreshModelChoices('session', true);
  await ctx.createSession({ preventDefault() {} });
  assert.equal(saved.provider_id, ''); assert.equal(saved.model, '');

  // 前一個 Provider 晚回來時，不可蓋掉使用者在新 Provider 選的模型。
  $('newProvider').value = 'slow'; const slow = ctx.refreshModelChoices('session', true);
  $('newProvider').value = 'fast'; const fast = ctx.refreshModelChoices('session', true);
  pending.get('fast')(); await fast;
  $('newModelCatalog').value = 'fast1'; ctx.selectModelChoice('session');
  pending.get('slow')(); await slow;
  assert.equal($('newModel').value, 'fast1');
  assert.ok(!$('newModelCatalog').options.some(o => o.value === 'slow1'));

  state.providerModelErrors.fast = '測試錯誤'; ctx.renderModelChoices('session');
  assert.match($('newModelState').textContent, /讀取失敗/);
  $('newModelCatalog').value = '__custom_model__'; ctx.selectModelChoice('session');
  assert.equal($('newModel').classList.hidden, false);
  $('newModel').value = 'custom-model'; ctx.renderModelChoices('session');
  await ctx.createSession({ preventDefault() {} });
  assert.equal(saved.model, 'custom-model');
  ctx.openSessionDialog('project');
  assert.equal($('newModel').value, '');
  assert.equal($('newModel').classList.hidden, true, '重新開啟應清除手動輸入狀態');
  assert.match(html, /<select id="newModelCatalog"/);
  assert.ok(source.includes('$("newModelCatalog").addEventListener("change", () => selectModelChoice("session"))'));
  console.log('PASS: Session Provider／模型清單、預設繼承、提交、切換競態、手動輸入與重新開啟');
})().catch(error => { console.error(error); process.exitCode = 1; });
