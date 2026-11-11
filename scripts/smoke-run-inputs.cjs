// 合成 API、SSE 與 DOM；不讀取設定、不呼叫模型、不啟動 APP。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const { randomUUID } = require('node:crypto');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
function extract(name) {
  const start = source.search(new RegExp(`^(?:async )?function ${name}\\(`, 'm'));
  assert.ok(start >= 0, name);
  const rest = source.slice(start + 1);
  const end = rest.search(/\n(?:async )?function /);
  return source.slice(start, end < 0 ? undefined : start + 1 + end);
}
function node() {
  return { children: [], textContent: '', attrs: {}, listeners: {},
    classList: { toggle() {}, add() {}, remove() {} },
    replaceChildren(...items) { this.children = items; },
    append(...items) { this.children.push(...items); },
    insertBefore(item, before) { this.children.splice(this.children.indexOf(before), 0, item); },
    setAttribute(key, value) { this.attrs[key] = value; },
    addEventListener(key, callback) { this.listeners[key] = callback; },
  };
}
const nodes = new Map();
const $ = id => { if (!nodes.has(id)) nodes.set(id, node()); return nodes.get(id); };
const state = { session: { id: 'session-a' }, promptQueue: [], activeRuns: new Map(), backendHealthy: true,
  restoreSkippedSessionIDs: new Set(), outboxAvailable: true };
const saved = new Map();
let run, requests, drains, finished, confirmation = false;
const ctx = vm.createContext({ state, $, document: { createElement: node }, crypto: { randomUUID },
  Date, Set, Map, JSON, Number, String, Array, Promise, TextDecoder, AbortController, setTimeout, clearTimeout,
  translate: x => x, toast() {}, syncNativeConversationActivity() {}, syncRunActionButton() {},
  syncRunStateAliases() {}, setRunActivity() {},
  activeRunFor: id => state.activeRuns.get(id || state.session?.id),
  selectedSessionIsRunning: () => Boolean(state.activeRuns.get(state.session?.id)),
  confirmAction: async () => confirmation,
  handleTerminalRun: value => { run.terminal = true; run.status = value.status; },
  finishActiveRun: async () => { finished++; },
});
for (const name of ['serializablePromptItem', 'promptUsesMemoryOnly', 'renderPromptQueue', 'enqueuePrompt',
  'insertQueuedPrompt', 'holdPendingPrompts', 'applyRunInputEvent', 'reconcileTerminalInputs',
  'reconcileSavedRunInputs', 'consumeEvents', 'cancelCurrentRun', 'retryQueuedPrompt', 'promptQueueCanAutoLaunch']) {
  vm.runInContext(extract(name), ctx);
}
ctx.savePromptOutboxItem = async item => { saved.set(item.id, JSON.parse(JSON.stringify(ctx.serializablePromptItem(item)))); };
ctx.deletePromptOutboxItem = async id => { saved.delete(id); };
ctx.drainPromptQueue = async () => { drains++; };
function reset() {
  state.promptQueue = []; state.session = { id: 'session-a' }; state.activeRuns.clear(); saved.clear();
  run = { runId: 'run-a', sessionId: 'session-a', status: 'running', eventController: new AbortController() };
  state.activeRuns.set(run.sessionId, run); requests = 0; drains = 0; finished = 0;
  ctx.request = async () => { requests++; return { id: 'input-a' }; };
  ctx.fetch = async () => sse([event('run.canceled')]);
}
const event = (type, payload = {}) => ({ run_id: 'run-a', type, payload });
const input = item => ({ id: 'input-a', client_id: item.id, input: item.input });
const applied = item => event('message.end', { message: { id: 'message-a', role: 'user', content: item.input,
  metadata: { inserted: true, input_id: 'input-a', client_id: item.id } } });
const sse = events => new Response(events.map((e, i) => `data: ${JSON.stringify({ ...e, sequence: i + 1 })}\n\n`).join(''));
async function waitForRequest(ready) {
  for (let i = 0; i < 10 && !ready(); i++) await new Promise(setImmediate);
  assert.ok(ready(), '插入請求應已送出');
}

(async () => {
  reset();
  await ctx.enqueuePrompt('保留排隊');
  let item = state.promptQueue[0];
  assert.equal(item.status, 'pending'); assert.equal(requests, 0, '送出不得自動插入');
  assert.ok($('promptQueue').children[0].children.some(n => n.textContent === '插入'));
  const first = ctx.insertQueuedPrompt(item, run);
  await ctx.insertQueuedPrompt(item, run); await first;
  assert.equal(requests, 1, '雙擊只能送一次'); assert.equal(item.status, 'waiting_insert');
  assert.equal(saved.get(item.id).insertionRunId, 'run-a');
  await ctx.applyRunInputEvent(applied(item), 'session-a');
  assert.equal(item.status, 'insert_applied');
  await ctx.applyRunInputEvent(event('run.completed'), 'session-a');
  assert.equal(state.promptQueue.length, 0); assert.equal(saved.size, 0);

  for (const terminal of ['run.completed', 'run.failed', 'run.canceled']) {
    reset(); await ctx.enqueuePrompt('尚未處理'); item = state.promptQueue[0];
    let resolve;
    ctx.request = () => new Promise(done => { resolve = done; });
    const sending = ctx.insertQueuedPrompt(item, run); await waitForRequest(() => resolve);
    await ctx.applyRunInputEvent(event(terminal, { returned_inputs: [input(item)] }), 'session-a');
    resolve(input(item)); await sending;
    assert.equal(item.status, 'returned', '較晚的 POST 回覆不可覆蓋退回狀態');
    assert.equal(ctx.promptQueueCanAutoLaunch(item), false);
    await ctx.applyRunInputEvent(event(terminal, { returned_inputs: [input(item)] }), 'session-a');
    assert.equal(state.promptQueue.length, 1, '重播退回事件不可重複建立訊息');
  }

  reset(); await ctx.enqueuePrompt('早到的套用事件'); item = state.promptQueue[0];
  let resolve;
  ctx.request = () => new Promise(done => { resolve = done; });
  const sending = ctx.insertQueuedPrompt(item, run); await waitForRequest(() => resolve);
  await ctx.applyRunInputEvent(applied(item), 'session-a');
  await ctx.applyRunInputEvent(event('run.completed'), 'session-a');
  resolve(input(item)); await sending;
  assert.equal(state.promptQueue.length, 0); assert.equal(saved.size, 0, 'POST 不可復活已消化的 outbox');

  reset(); await ctx.enqueuePrompt('插入拒絕'); item = state.promptQueue[0];
  ctx.request = async () => { throw Object.assign(new Error('conflict'), { status: 409 }); };
  await ctx.insertQueuedPrompt(item, run);
  assert.equal(item.status, 'pending'); assert.equal(item.insertionRunId, '');
  ctx.request = async () => { throw new Error('response lost'); };
  await ctx.insertQueuedPrompt(item, run);
  assert.equal(item.status, 'insert_unknown'); assert.equal(ctx.promptQueueCanAutoLaunch(item), false);
  await ctx.retryQueuedPrompt(item.id); assert.equal(item.status, 'insert_unknown');
  confirmation = true; await ctx.retryQueuedPrompt(item.id);
  assert.equal(item.status, 'pending'); assert.equal(item.insertionRunId, ''); confirmation = false;

  reset(); await ctx.enqueuePrompt('附件', { attachmentIds: ['attachment-a'] }); item = state.promptQueue[0];
  assert.ok(!$('promptQueue').children[0].children.some(n => n.textContent === '插入'));
  await ctx.insertQueuedPrompt(item, run); assert.equal(requests, 0);
  await ctx.enqueuePrompt('停止後保留'); const queued = state.promptQueue[1];
  ctx.request = async () => ({ id: run.runId, status: 'canceled' });
  await ctx.cancelCurrentRun();
  assert.equal(item.status, 'returned'); assert.equal(queued.status, 'returned'); assert.equal(finished, 1);

  reset(); await ctx.enqueuePrompt('先補退回事件再關閉串流'); item = state.promptQueue[0];
  await ctx.insertQueuedPrompt(item, run);
  const terminalEvent = event('run.canceled', { returned_inputs: [input(item)] });
  ctx.request = async () => ({ id: run.runId, status: 'canceled' });
  ctx.fetch = async () => { assert.equal(run.eventController.signal.aborted, false); return sse([terminalEvent]); };
  await ctx.cancelCurrentRun();
  assert.equal(item.status, 'returned'); assert.equal(run.eventController.signal.aborted, true);

  reset(); await ctx.enqueuePrompt('重新載入後確認'); item = state.promptQueue[0];
  item.insertionRunId = 'run-a'; item.status = 'insert_unknown';
  state.session = { id: 'session-b' };
  ctx.request = async () => ({ id: 'run-a', status: 'completed' });
  ctx.fetch = async () => sse([applied(item), event('run.completed')]);
  await ctx.reconcileSavedRunInputs(); assert.equal(state.promptQueue.length, 0, '背景對話套用事件也要同步');
  await ctx.applyRunInputEvent(event('run.canceled', { returned_inputs: [{ id: 'remote', input: '另一視窗插入' }] }), 'session-a');
  assert.equal(state.promptQueue[0].sessionId, 'session-a'); assert.equal(state.promptQueue[0].status, 'returned');

  reset(); await ctx.enqueuePrompt('停止與按鈕競態'); item = state.promptQueue[0];
  run.canceling = true;
  await ctx.insertQueuedPrompt(item, run); assert.equal(requests, 0);
  await ctx.enqueuePrompt('停止途中新輸入'); assert.equal(state.promptQueue[1].status, 'returned');

  reset(); await ctx.enqueuePrompt('等待時不准移除'); item = state.promptQueue[0];
  await ctx.insertQueuedPrompt(item, run);
  assert.equal($('promptQueue').children[0].children.at(-1).disabled, true);
  ctx.fetch = async () => { throw new Error('offline'); };
  await ctx.reconcileTerminalInputs({ id: 'run-a', status: 'completed' }, 'session-a');
  assert.equal(item.status, 'insert_unknown'); assert.equal(ctx.promptQueueCanAutoLaunch(item), false);

  reset(); await ctx.enqueuePrompt('存檔失敗不可送 API'); item = state.promptQueue[0];
  const save = ctx.savePromptOutboxItem;
  ctx.savePromptOutboxItem = async () => { throw new Error('storage unavailable'); };
  await ctx.insertQueuedPrompt(item, run); assert.equal(requests, 0); assert.equal(item.status, 'failed');
  ctx.savePromptOutboxItem = save;

  reset(); await ctx.enqueuePrompt('真正套用後才顯示'); item = state.promptQueue[0];
  await ctx.insertQueuedPrompt(item, run);
  vm.runInContext(extract('handleEvent'), ctx);
  const messages = new Map();
  ctx.findMessage = id => messages.get(id);
  ctx.appendMessage = message => messages.set(message.id, message);
  ctx.setAgentProcessing = () => {};
  ctx.scrollMessages = () => {};
  await ctx.handleEvent(applied(item), 'session-a');
  assert.equal(messages.size, 1); assert.equal(run.runDraft, null);
  await ctx.handleEvent(applied(item), 'session-a');
  assert.equal(messages.size, 1, '重播套用訊息不可多畫一份');
  run.terminalHandled = true;
  await ctx.handleEvent(event('run.canceled', { returned_inputs: [{ id: 'late', client_id: 'late-client', input: '晚到退回' }] }), 'session-a');
  assert.equal(state.promptQueue.find(i => i.id === 'late-client').status, 'returned', '本機已收尾仍須接收退回事件');

  // 插入中的已持久化內容重新載入後不能變回可自動送出的 pending。
  vm.runInContext(extract('loadPromptOutbox'), ctx);
  ctx.outboxTransaction = async mode => mode === 'readonly' ? [{ id: 'reload', sessionId: 'session-a',
    input: 'reload', insertionRunId: 'run-a', status: 'inserting', createdAt: new Date().toISOString() }] : undefined;
  state.backendHealthy = false;
  await ctx.loadPromptOutbox();
  assert.equal(state.promptQueue[0].status, 'insert_unknown');
  assert.equal(ctx.promptQueueCanAutoLaunch(state.promptQueue[0]), false);
  state.backendHealthy = true;

  vm.runInContext(extract('request'), ctx);
  ctx.transientStatuses = new Set([503]);
  ctx.fetch = async () => new Response(JSON.stringify({ detail: 'conflict' }), { status: 409 });
  await assert.rejects(ctx.request('/synthetic', { method: 'POST', reconnects: 0 }), error => error.status === 409);

  // 隔離 Session 的新增欄位也沿用原本不落盤的規則。
  vm.runInContext(extract('savePromptOutboxItem'), ctx);
  let writes = 0; ctx.outboxTransaction = async () => { writes++; };
  await ctx.savePromptOutboxItem({ sessionId: 'session_vabcd_example', status: 'waiting_insert', input: 'volatile' });
  assert.equal(writes, 0);
  console.log('PASS: 保留排隊、手動插入、雙擊、事件競態、409、未知結果、附件、停止退回、背景恢復與 RAM 隔離');
})().catch(error => { console.error(error); process.exitCode = 1; });
