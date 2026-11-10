// 使用合成管理狀態與 API 回覆，不讀取或修改使用者設定。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.join(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/web/console/assets/app.js'), 'utf8');
const html = fs.readFileSync(path.join(root, 'src/web/console/assets/index.html'), 'utf8');
assert.ok(html.indexOf('id="toast"') > html.lastIndexOf('</main>'), '提示不可放在管理頁隱藏的聊天區');
const nodes = new Map();
const $ = id => {
  if (!nodes.has(id)) nodes.set(id, { textContent: '', disabled: false, checked: false, reportValidity: () => true, classList: { toggle(name, value) { this[name] = value; } } });
  return nodes.get(id);
};
let requests = 0, apiError = '', refreshError = false, rendered = 0, sessionsLoaded = 0;
let provider;
const state = {};
const ctx = vm.createContext({ $, state, providerSettingFormValue: () => provider,
  toast: () => {}, confirmAction: async () => true,
  renderProviderSettings: () => { rendered++; },
  loadSessions: async () => { sessionsLoaded++; },
  refreshProviderCatalog: async () => { if (refreshError) throw new Error('synthetic refresh failure'); },
  request: async (_url, options) => {
    requests++;
    if (apiError) throw new Error(apiError);
    return JSON.parse(options.body);
  },
});
vm.runInContext(source.slice(source.indexOf('function providerSettingsPayload('), source.indexOf('function updateProviderOAuthStatusUI(')), ctx);
function reset() {
  requests = 0; apiError = ''; refreshError = false; rendered = 0; sessionsLoaded = 0;
  provider = { id: 'codex', type: 'openai-codex-responses', enabled: true, openai_codex_responses: { model: 'synthetic' } };
  Object.assign(state, { providerSettings: { default_provider_id: 'primary', providers: [{ id: 'primary' }, provider] }, selectedProviderSettingsID: 'codex', providerSettingsDraft: null, providerModelLists: {}, providerModelErrors: {}, providerTestedModels: {} });
}
(async () => {
  reset();
  await ctx.persistProviderSetting({ refreshModels: false });
  assert.equal(requests, 1, '未登入、無模型目錄仍可儲存既有設定');
  assert.equal($('providerSettingsState').textContent, '已套用');
  reset();
  await ctx.deleteProviderSetting();
  assert.equal(state.providerSettings.providers.length, 1);
  assert.equal(rendered, 1);
  assert.equal(sessionsLoaded, 1, '刪除後須重新取得 Session 的預設選擇');
  for (const operation of ['persistProviderSetting', 'deleteProviderSetting']) {
    reset(); apiError = 'synthetic provider is still used by workspace';
    await ctx[operation]({ refreshModels: false });
    assert.ok($('providerSettingsError').textContent.includes(apiError));
    assert.equal($('providerSettingsError').classList.hidden, false);
    assert.equal(state.providerSettings.providers.length, 2);
    reset(); refreshError = true;
    await ctx[operation]({ refreshModels: false });
    assert.match($('providerSettingsState').textContent, /已(儲存|刪除) · 畫面更新失敗/);
    assert.equal(rendered, 1, '寫入成功必須先更新管理畫面');
  }
  reset(); provider.openai_codex_responses.model = '';
  await ctx.persistProviderSetting();
  assert.equal(requests, 0);
  assert.equal($('providerSettingsError').classList.hidden, false);
  reset(); $('providerSettingsForm').reportValidity = () => false;
  await ctx.persistProviderSetting();
  assert.equal(requests, 0);
  assert.match($('providerSettingsError').textContent, /欄位驗證未通過/);
  console.log('PASS: Provider 儲存／刪除、可見錯誤、缺少模型與更新畫面失敗');
})().catch(error => { console.error(error); process.exitCode = 1; });
