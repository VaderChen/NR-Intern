// 合成錯誤，確認測試原因不會只存在短暫 toast。
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const nodes = new Map();
const $ = id => {
  if (!nodes.has(id)) nodes.set(id, { textContent: '', disabled: false, classList: { toggle(name, value) { this[name] = value; } } });
  return nodes.get(id);
};
const ctx = vm.createContext({ $, persistProviderSetting: async () => ({id: 'synthetic'}), request: async () => { throw new Error('synthetic tool_choice rejected'); }, toast: () => {} });
vm.runInContext(source.slice(source.indexOf('function showProviderSettingsError('), source.indexOf('async function persistProviderSetting(')), ctx);
vm.runInContext(source.slice(source.indexOf('async function testProviderSetting()'), source.indexOf('async function deleteProviderSetting()')), ctx);
(async () => {
  await ctx.testProviderSetting();
  assert.match($('providerSettingsError').textContent, /synthetic tool_choice rejected/);
  assert.equal($('providerSettingsError').classList.hidden, false);
  assert.equal($('testProviderSetting').disabled, false);
  assert.equal($('saveProviderSetting').disabled, false);
  console.log('PASS: 測試錯誤持續顯示並恢復按鈕');
})().catch(error => { console.error(error); process.exitCode = 1; });
