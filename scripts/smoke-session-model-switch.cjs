// 合成執行中 Session；不啟動 APP 或呼叫上游。
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const nodes = new Map();
const $ = id => { if (!nodes.has(id)) nodes.set(id, { setAttribute() {} }); return nodes.get(id); };
const state = { session: { id: 's', provider_id: 'p' }, backendHealthy: true, sessionRuntimeSaving: false };
let calls = [];
const ctx = vm.createContext({ state, $, selectedSessionIsRunning: () => true,
  sessionRuntimeModels: () => ['old', 'new'], updateSessionRuntime: async (id, patch) => calls.push({ id, patch }) });
for (const name of ['syncSessionRuntimeControlState', 'changeSessionModel', 'changeSessionThinking']) {
  const start = source.search(new RegExp(`^(?:async )?function ${name}\\(`, 'm'));
  const end = source.slice(start + 1).search(/\n(?:async )?function /);
  vm.runInContext(source.slice(start, start + 1 + end), ctx);
}
(async () => {
  ctx.syncSessionRuntimeControlState();
  assert.equal($('sessionModelSelect').disabled, false);
  assert.equal($('sessionProviderSelect').disabled, true);
  assert.equal($('sessionThinkingSelect').disabled, false);
  await ctx.changeSessionModel({ target: { value: 'new' } });
  assert.equal(calls.length, 1); assert.equal(calls[0].patch.model, 'new');
  await ctx.changeSessionThinking({ target: { value: 'high' } });
  assert.equal(calls[1].patch.thinking_mode, 'high');
  await ctx.changeSessionThinking({ target: { value: '' } });
  assert.equal(calls[2].patch.thinking_mode, '', '可切回自動');
  state.sessionRuntimeSaving = true; ctx.syncSessionRuntimeControlState();
  assert.equal($('sessionModelSelect').disabled, true);
  await ctx.changeSessionModel({ target: { value: 'old' } });
  await ctx.changeSessionThinking({ target: { value: 'low' } });
  assert.equal(calls.length, 3);
  console.log('PASS: 執行中模型可選、其他設定保護、修改送出與重複提交防護');
})().catch(error => { console.error(error); process.exitCode = 1; });
