// 只用合成 Run 與網路回應，不啟動 APP、不連線到使用者後端。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const run = { runId:'run-smoke', sessionId:'session-smoke', runDraft:{}, terminal:false };
let reads = 0, removed = false;
const context = vm.createContext({
  activeRunFor: () => run,
  state: {},
  removeActiveRun: () => { removed = true; return false; },
  syncRunStateAliases() {}, syncRunActionButton() {}, setRunActivity() {}, showApproval() {},
  handleTerminalRun: (value) => { run.terminal = true; run.status = value.status; },
  reconcileTerminalInputs: async () => {},
  request: async () => (++reads >= 5 ? {status:'completed'} : {status:'running'}),
});
for (const [start, end] of [
  ['async function finishActiveRun(', 'function syncSessionUI('],
  ['async function runWithReconnect(', 'function waitForRunReconnect('],
]) {
  const begin = source.indexOf(start), finish = source.indexOf(end, begin);
  assert.ok(begin >= 0 && finish > begin);
  vm.runInContext(source.slice(begin, finish), context);
}
context.openRunStream = async () => { throw new Error('synthetic disconnection'); };
context.waitForRunReconnect = async () => {};
(async () => {
  await context.finishActiveRun(run);
  assert.equal(removed, false, 'unknown Run must remain active');
  await context.runWithReconnect({runId:run.runId, sessionId:run.sessionId, runState:run});
  assert.equal(reads, 5, 'must continue beyond three reconnects');
  assert.equal(run.terminal, true);
  await context.finishActiveRun(run);
  assert.equal(removed, true);
  console.log('PASS: 斷線保留 Run、持續重連、確認終態才清理');
})().catch(error => { console.error(error); process.exitCode = 1; });
