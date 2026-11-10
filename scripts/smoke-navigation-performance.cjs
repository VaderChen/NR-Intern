// 僅使用合成 Session 與 DOM 替身，不啟動 APP 或讀取使用者資料。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { performance } = require('node:perf_hooks');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const start = source.indexOf('function renderNavigation(');
const end = source.indexOf('function projectNode(', start);
assert.ok(start >= 0 && end > start);
const element = () => ({
  children: [], classList: { toggle() {} }, setAttribute() {}, querySelector() { return this; },
  replaceChildren(...values) { this.children = values; }, append(...values) { this.children.push(...values); },
});
const nodes = new Map();
const context = vm.createContext({
  state: {}, document: { createElement: element },
  $: id => { if (!nodes.has(id)) nodes.set(id, element()); return nodes.get(id); },
  sessionNode: session => session.id,
  projectNode: (project, sessions) => ({ id: project?.id ?? null, sessions: sessions.map(session => session.id) }),
  renderSchedules() {},
});
vm.runInContext(source.slice(start, end), context);
function reference(state) {
  const ordered = sessions => sessions.map((session, index) => ({ session, index }))
    .sort((a, b) => {
      const left = Number.isFinite(Number(a.session.position)) ? Number(a.session.position) : 0;
      const right = Number.isFinite(Number(b.session.position)) ? Number(b.session.position) : 0;
      return left - right || a.index - b.index;
    }).map(value => value.session.id);
  const pinned = state.sessions.filter(session => session.pinned)
    .sort((a, b) => new Date(b.pinned_at || b.updated_at) - new Date(a.pinned_at || a.updated_at)).map(session => session.id);
  const groups = state.projectSectionCollapsed ? [] : state.projects.map(project => ({
    id: project.id, sessions: ordered(state.sessions.filter(session => session.project_id === project.id && !session.pinned)),
  }));
  const ungrouped = ordered(state.sessions.filter(session => !session.project_id && !session.pinned));
  if (!state.projectSectionCollapsed && ungrouped.length) groups.push({ id: null, sessions: ungrouped });
  return { pinned, groups };
}
const positions = [undefined, 0, 1, -1, '2', 'invalid', null, 1];
for (let sample = 0; sample < 100; sample++) {
  context.state = {
    sessions: Array.from({ length: 100 + sample }, (_, i) => ({
      id: `session_${i}`, project_id: [undefined, '', null, 'one', 'two', 'absent'][i % 6],
      pinned: (i + sample) % 11 === 0, position: positions[(i + sample) % positions.length],
      updated_at: `2026-01-${String(i % 28 + 1).padStart(2, '0')}`,
    })),
    projects: [{ id: 'one' }, { id: 'two' }, { id: 'empty' }],
    projectSectionCollapsed: sample % 5 === 0,
  };
  const expected = reference(context.state);
  const original = JSON.stringify(context.state);
  context.renderNavigation();
  assert.deepEqual(Array.from(nodes.get('pinnedList').children), expected.pinned);
  assert.deepEqual(JSON.parse(JSON.stringify(nodes.get('projectList').children)), expected.groups);
  assert.equal(JSON.stringify(context.state), original, '導覽排序不可修改 Session 原始陣列');
}
context.state = {
  sessions: Array.from({ length: 10_000 }, (_, i) => ({ id: `s${i}`, project_id: `p${i % 500}`, position: 10_000 - i })),
  projects: Array.from({ length: 500 }, (_, i) => ({ id: `p${i}` })),
};
const samples = [];
for (let sample = 0; sample < 4; sample++) {
  const begin = performance.now();
  context.renderNavigation();
  if (sample) samples.push(Number((performance.now() - begin).toFixed(3)));
}
console.log(`PASS: 導覽分組與排序 100 組相容性；500 專案／一萬 Session 合成耗時（ms）=${samples.join(', ')}`);
