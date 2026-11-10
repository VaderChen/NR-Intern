// 合成 DOM／模型目錄，驗證選擇、儲存與非同步切換；不連線真實上游。
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
class Option { constructor(text, value) { this.text = text; this.value = value; } }
const nodes = new Map();
const $ = id => {
  if (!nodes.has(id)) nodes.set(id, { value: '', dataset: {}, options: [], textContent: '', disabled: false,
    replaceChildren(...items) { this.options = items; }, add(item) { this.options.push(item); }, focus() {}, select() {},
    classList: { toggle(name, value) { this[name] = value; } },
  });
  return nodes.get(id);
};
const workspace = { id: 'w', default_provider_id: 'a', provider_ids: ['a'], model: 'custom' };
const state = { workspace, workspaces: [workspace], providerModelLists: { a: ['a1', 'a2'], b: ['b1'] }, providerModelErrors: {} };
const pending = new Map();
let delayed = false, saved = null, writes = 0;
const ctx = vm.createContext({ $, Option, state,
  loadProviderModels: async id => {
    if (delayed) await new Promise(resolve => pending.set(id, resolve));
    state.providerModelLists[id] ||= [id + '1'];
  },
  request: async (_url, options) => { writes++; saved = JSON.parse(options.body); return { id: 'w', ...saved }; },
  renderNavigation() {}, syncSessionUI() {}, renderWorkspaceOptions() {}, syncWorkspaceSelectorValues() {}, syncWorkspaceSettings() {}, toast() {},
});
vm.runInContext(source.slice(source.indexOf('async function initializeWorkspaceModels('), source.indexOf('function sessionRuntimeModels(')), ctx);
vm.runInContext(source.slice(source.indexOf('async function saveWorkspaceSettings('), source.indexOf('// saveServiceSettingPatch')), ctx);
(async () => {
  for (const mode of ['setting', 'new']) {
    const prefix = mode + 'Workspace';
    $(prefix + 'Provider').value = 'a';
    $(prefix + 'Model').value = 'custom';
    await ctx.refreshWorkspaceModelChoices(mode);
    assert.deepEqual($(prefix + 'ModelCatalog').options.map(o => o.value), ['', 'a1', 'a2', 'custom', '__custom_model__']);
    $(prefix + 'ModelCatalog').value = 'a2'; ctx.selectWorkspaceModel(mode);
    assert.equal($(prefix + 'Model').value, 'a2');
    $(prefix + 'ModelCatalog').value = '__custom_model__'; ctx.selectWorkspaceModel(mode);
    assert.equal($(prefix + 'Model').classList.hidden, false);
    $(prefix + 'Model').value = 'typed-model';
    ctx.renderWorkspaceModelChoices(mode);
    assert.equal($(prefix + 'Model').value, 'typed-model');
    $(prefix + 'Provider').value = 'b';
    await ctx.refreshWorkspaceModelChoices(mode, true);
    assert.equal($(prefix + 'Model').value, '');
    assert.ok($(prefix + 'ModelCatalog').options.some(o => o.value === 'b1'));
  }
  // 快速切換時，舊 Provider 晚回來不得蓋掉新選擇。
  delayed = true;
  $('settingWorkspaceProvider').value = 'slow';
  const slow = ctx.refreshWorkspaceModelChoices('setting', true);
  $('settingWorkspaceProvider').value = 'fast';
  const fast = ctx.refreshWorkspaceModelChoices('setting', true);
  pending.get('fast')(); await fast;
  $('settingWorkspaceModelCatalog').value = 'fast1'; ctx.selectWorkspaceModel('setting');
  pending.get('slow')(); await slow;
  assert.equal($('settingWorkspaceModel').value, 'fast1');
  assert.ok(!$('settingWorkspaceModelCatalog').options.some(o => o.value === 'slow1'));
  delayed = false;
  await ctx.initializeWorkspaceModels('w');
  assert.equal(writes, 0, '載入目錄不得自動 PATCH Workspace');
  assert.equal($('settingWorkspaceModel').value, 'fast1');
  await ctx.saveWorkspaceSettings({ preventDefault() {} });
  assert.equal(saved.model, 'fast1');
  assert.equal(saved.default_provider_id, 'fast');
  state.providerModelErrors.fast = 'synthetic unavailable';
  ctx.renderWorkspaceModelChoices('setting');
  assert.match($('settingWorkspaceModelState').textContent, /讀取失敗/);
  assert.equal($('settingWorkspaceModel').value, 'fast1');
  console.log('PASS: Workspace 模型選擇、手動輸入、切換競態、儲存與載入失敗保留設定');
})().catch(error => { console.error(error); process.exitCode = 1; });
