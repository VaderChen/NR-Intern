// 合成文字／DOM 替身；不啟動 APP，也不載入使用者對話。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { performance } = require('node:perf_hooks');

const app = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const context = vm.createContext({});
const start = app.indexOf('function reasoningSegmentKey(');
const end = app.indexOf('function appendMessage(', start);
assert.ok(start >= 0 && end > start);
vm.runInContext(app.slice(start, end), context);
const blocks = Array.from({ length: 1200 }, (_, index) => `第 ${index} 段：合成的執行過程與狀態說明。`);
const input = blocks.join('\n\n');
const samples = [];
for (let run = 0; run < 3; run += 1) {
  const started = performance.now();
  assert.equal(context.dedupeRepeatedReasoningBlocks(input), input);
  samples.push(Number((performance.now() - started).toFixed(2)));
}
assert.equal(context.dedupeRepeatedReasoningBlocks('標題\n\n說明\n\n標題\n\n說明'), '標題\n\n說明');
assert.equal(context.dedupeRepeatedReasoningBlocks('A\n\nB\n\nA\n\nC'), 'A\n\nB\n\nA\n\nC');
assert.equal(context.dedupeRepeatedReasoningBlocks('相同  空白\n\n相同\t空白'), '相同  空白');
assert.equal(context.appendReasoningDelta('我', '我'), '我');
assert.equal(context.appendReasoningDelta('這是正常文字', '字'), '這是正常文字字');
console.log(`PASS: 思考內容去重相容性；1200 段合成資料耗時（ms）=${samples.join(', ')}`);

// 與原本的最長後綴比對逐項比較，包含不同長度的週期與相同空白正規化。
function referenceDedupe(value) {
  const blocks = String(value || '').trim().split(/\n{2,}/u).map(block => block.trim()).filter(Boolean);
  const result = [];
  for (let index = 0; index < blocks.length;) {
    let repeated = 0;
    for (let length = Math.min(result.length, blocks.length - index); length > 0; length -= 1) {
      if (Array.from({ length }, (_, offset) => offset).every(offset =>
        context.reasoningSegmentKey(result[result.length - length + offset]) === context.reasoningSegmentKey(blocks[index + offset]))) {
        repeated = length;
        break;
      }
    }
    if (repeated) index += repeated;
    else result.push(blocks[index++]);
  }
  return result.join('\n\n');
}
let seed = 123456;
const next = () => (seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0);
const words = ['A', 'B', 'C', '重複  空白', '重複\t空白', '不同段落', ''];
for (let sample = 0; sample < 400; sample += 1) {
  const text = Array.from({ length: next() % 100 }, () => words[next() % words.length]).join('\n\n');
  assert.equal(context.dedupeRepeatedReasoningBlocks(text), referenceDedupe(text));
}

const pending = new Map();
let frameID = 0, parseCalls = 0, sanitizeCalls = 0, mathCalls = 0;
const parser = text => { parseCalls += 1; return `<p>${text}</p>`; };
const rendererContext = vm.createContext({
  window: {
    marked: { parse: parser },
    DOMPurify: { sanitize: (html, options) => {
      sanitizeCalls += 1;
      assert.equal(options.ALLOW_DATA_ATTR, false);
      assert.ok(options.FORBID_TAGS.includes('script'));
      return html;
    } },
    renderMathInElement: () => { mathCalls += 1; },
  },
  document: { createTreeWalker: () => ({ nextNode: () => null }) },
  NodeFilter: { SHOW_TEXT: 4 },
  requestAnimationFrame: callback => { const id = ++frameID; pending.set(id, callback); return id; },
  cancelAnimationFrame: id => pending.delete(id),
});
vm.runInContext(fs.readFileSync(path.join(__dirname, '../src/web/console/assets/rich-text.js'), 'utf8'), rendererContext);
const renderer = rendererContext.window.RichTextRenderer;
const makeElement = () => ({ innerHTML: '', textContent: '', querySelectorAll: () => [] });
const flush = () => {
  const callbacks = [...pending.values()];
  pending.clear();
  callbacks.forEach(callback => callback());
};
const element = makeElement();
renderer.render(element, 'first');
assert.equal(parseCalls, 1);
renderer.render(element, 'first');
renderer.schedule(element, 'first');
assert.equal(parseCalls, 1);
assert.equal(pending.size, 0);
renderer.schedule(element, 'second');
renderer.schedule(element, 'third');
assert.equal(pending.size, 1);
assert.equal(renderer.source(element), 'third');
assert.equal(element.innerHTML, '<p>first</p>');
flush();
assert.equal(element.innerHTML, '<p>third</p>');
assert.equal(parseCalls, 2);
renderer.schedule(element, 'pending');
renderer.schedule(element, 'third');
assert.equal(pending.size, 0);
renderer.schedule(element, 'final');
renderer.render(element, 'final');
assert.equal(pending.size, 0);
assert.equal(element.innerHTML, '<p>final</p>');
assert.equal(parseCalls, sanitizeCalls);
assert.equal(parseCalls, mathCalls);
renderer.render(makeElement(), 'final');
assert.equal(parseCalls, 4, '不同節點不得共用已渲染標記');

// Markdown 暫時不可用時回退純文字，但後續仍能以相同來源重試。
rendererContext.window.marked.parse = () => { throw new Error('synthetic parser unavailable'); };
const fallback = makeElement();
renderer.render(fallback, 'fallback');
assert.equal(fallback.textContent, 'fallback');
rendererContext.window.marked.parse = parser;
renderer.render(fallback, 'fallback');
assert.equal(fallback.innerHTML, '<p>fallback</p>');

const reasoningElement = makeElement();
const block = { querySelector: () => reasoningElement, dataset: {}, classList: { remove() {} } };
const message = { querySelector: () => block, dataset: { operationId: 'synthetic' } };
Object.assign(context, {
  richContentSource: renderer.source,
  renderRichContent: renderer.render,
  scheduleRichContent: renderer.schedule,
  updateReasoningSummary() {}, updateLiveReasoningDuration() {},
});
const reasonStart = app.indexOf('const reasoningRenderInputs =');
const reasonEnd = app.indexOf('function mergeReasoningIntoPrevious(', reasonStart);
assert.ok(reasonStart >= 0 && reasonEnd > reasonStart);
vm.runInContext(app.slice(reasonStart, reasonEnd), context);
const beforeReasoning = parseCalls;
context.setMessageReasoning(message, '思考內容', false, true);
context.setMessageReasoning(message, '思考內容', false, true);
assert.equal(parseCalls, beforeReasoning);
assert.equal(pending.size, 1);
context.setMessageReasoning(message, '思考內容');
assert.equal(pending.size, 0, '收尾必須沖出尚未渲染的最後一幀');
assert.equal(reasoningElement.innerHTML, '<p>思考內容</p>');
assert.equal(parseCalls, beforeReasoning + 1);
context.setMessageReasoning(message, '另一階段', true);
flush();
assert.ok(reasoningElement.innerHTML.includes('另一階段'));
context.setMessageReasoning(message, '思考內容');
assert.equal(reasoningElement.innerHTML, '<p>思考內容</p>', '追加後必須使原始輸入快取失效');
context.reasoningElement = reasoningElement;
assert.equal(vm.runInContext('reasoningRenderInputs.has(reasoningElement)', context), false, '收尾後不得保留去重前的原始文字');
console.log('PASS: 400 組去重比對、重複內容略過渲染、同幀合併、收尾沖出、回退重試與追加快取失效');
