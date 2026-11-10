// 合成表單與原始碼函式驗證，不讀取金鑰或存取 API。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const elements = new Map();
const $ = id => {
  if (!elements.has(id)) elements.set(id, { value: '', checked: false, disabled: false, classList: { toggle(k,v) { this[k]=v; } }, getAttribute() { return null; } });
  return elements.get(id);
};
const ctx = vm.createContext({$});
for (const [start,end] of [['function renderProviderResponseFields()', 'function renderProviderTypeFields('], ['function providerSettingFormValue()', 'function providerSettingsPayload(']]) {
  vm.runInContext(source.slice(source.indexOf(start),source.indexOf(end,source.indexOf(start))),ctx);
}
$('providerSettingType').value='claude-platform';
$('providerSettingUseResponses').checked=true;
$('providerSettingBaseURL').value='https://api.anthropic.com/v1';
ctx.renderProviderResponseFields();
assert.equal($('providerSettingUseResponses').checked,false);
assert.equal($('providerSettingUseResponses').disabled,true);
assert.equal($('providerSettingStreamUsageField').classList.hidden,true);
const value=ctx.providerSettingFormValue();
assert.equal(value.type,'openai-compatible');
assert.equal(value.openai_compatible.use_claude,true);
assert.equal(value.openai_compatible.use_responses,false);
assert.equal(value.openai_compatible.api_key,undefined);
$('providerSettingType').value='openai-compatible';ctx.renderProviderResponseFields();
assert.equal($('providerSettingUseResponses').disabled,false);
assert.equal(ctx.providerSettingFormValue().openai_compatible.use_claude,false);
assert.ok(source.includes('use_claude: source.use_claude === true'));
assert.ok(source.includes('use_claude: item.use_claude === true'));
console.log('PASS: Claude 表單、協定互斥、金鑰保留與匯入欄位');
