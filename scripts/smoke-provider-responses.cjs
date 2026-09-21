// 僅以合成 DOM 檢查協定切換與表單資料，不讀取設定或連線上游。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const elements = new Map();
const $ = id => {
  if (!elements.has(id)) elements.set(id, {
    value: '', checked: false, dataset: {},
    getAttribute() { return null; },
    classList: { toggle(name, enabled) { this[name] = enabled; } },
  });
  return elements.get(id);
};
const context = vm.createContext({ $ });
for (const [start, end] of [
  ['function renderProviderResponseFields()', 'function renderProviderTypeFields('],
  ['function providerSettingFormValue()', 'function providerSettingsPayload('],
]) {
  const begin = source.indexOf(start), finish = source.indexOf(end, begin);
  assert.ok(begin >= 0 && finish > begin);
  vm.runInContext(source.slice(begin, finish), context);
}
$('providerSettingType').value = 'openai-compatible';
$('providerSettingBaseURL').value = 'https://example.invalid/custom/responses?route=smoke';
for (const enabled of [true, false, true]) {
  $('providerSettingUseResponses').checked = enabled;
  context.renderProviderResponseFields();
  assert.equal($('providerSettingDisableStreamingField').classList.hidden, enabled);
  assert.equal($('providerSettingResponsesHint').classList.hidden, !enabled);
  const result = context.providerSettingFormValue();
  assert.equal(result.openai_compatible.use_responses, enabled);
  assert.equal(result.openai_compatible.base_url, 'https://example.invalid/custom/responses?route=smoke');
}
$('providerSettingType').value = 'openai-codex-responses';
context.renderProviderResponseFields();
assert.equal($('providerSettingResponsesHint').classList.hidden, true);
assert.equal(context.providerSettingFormValue().openai_compatible, undefined);
assert.ok(source.includes('$("providerSettingUseResponses").addEventListener("change", renderProviderResponseFields)'));
assert.equal(source.split('$("providerSettingUseResponses").checked = Boolean(settings.use_responses);').length - 1, 2);
assert.ok(source.includes('use_responses: source.use_responses === true'));
assert.ok(source.includes('use_responses: item.use_responses === true'));
console.log('PASS: Responses 開關、表單、URL 保留、OAuth 隔離及匯入欄位');
