// 用量呈現與快速切換驗證；合成快照，不呼叫上游。
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const assert = require('node:assert/strict');
const source = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/app.js'), 'utf8');
const nodes = new Map();
function node() { return { textContent:'', title:'', style:{}, children:[], attrs:{},
  classList:{add(k){this[k]=true;},remove(k){this[k]=false;},toggle(k,v){this[k]=v;},contains(k){return this[k]===true;}},
  replaceChildren(...v){this.children=v;},append(...v){this.children.push(...v);},setAttribute(k,v){this.attrs[k]=v;},removeAttribute(k){delete this.attrs[k];} }; }
const $=id=>{if(!nodes.has(id))nodes.set(id,node());return nodes.get(id);};
let provider='codex', model='model-a';
const pending=[];
const state={session:{id:'s'},backendHealthy:true,providerUsageRequest:0,providerUsagePending:'',providerUsageIdentity:''};
const ctx=vm.createContext({state,$,window:{},document:{hidden:false,createElement:node},Date,Intl,
  activeProviderID:()=>provider,activeContextIdentity:()=>({model}),
  request:async()=>new Promise(resolve=>pending.push(resolve)),
});
vm.runInContext(source.slice(source.indexOf('function formatProviderUsagePercent('),source.indexOf('function readMemo(')),ctx);
const now=Date.now();const updated_at=new Date(now).toISOString();const reset_at=new Date(now+60000).toISOString();
const quota={kind:'quota',updated_at,five_hour:{available:true,remaining_percent:80,reset_at},seven_day:{available:true,remaining_percent:25,reset_at}};
assert.equal(ctx.providerUsageView(quota,model,now).value,'25%');
const past=new Date(now-1000).toISOString();
const rate={kind:'rate_limit',model,updated_at,rate_limits:[{resource:'requests',limit:200,remaining:1,reset_at:past},{resource:'input_tokens',limit:1000,remaining:0,reset_at},{resource:'output_tokens',limit:1000,remaining:1000,reset_at},{resource:'tokens',limit:2000,remaining:500,reset_at}]};
assert.equal(ctx.providerUsageView(quota,model,now).hidden,false);
assert.equal(ctx.providerUsageView(null,model,now).value,'未知');
assert.equal(ctx.providerUsageView(null,model,now).hidden,false,'尚未取得快照時維持顯示未知');
assert.match(ctx.providerUsageView(quota,model,now+700000).value,/\*$/,'逾時或已過重設時間即標示非即時');
// Claude 的每分鐘速率額度不是組織點數：不論數值、模型或是否過期都不顯示。
for (const usage of [rate,{...rate,model:'other'},{...rate,rate_limits:[]},{...rate,updated_at:undefined},{...rate,rate_limits:[{resource:'tokens',remaining:50,reset_at}]}]) {
  const view=ctx.providerUsageView(usage,model,now);
  assert.equal(view.hidden,true);
  assert.equal(view.rows.length,0);
  assert.equal(view.value,'');
}
ctx.renderProviderUsage(rate);
assert.equal($('providerUsageSummaryControl').classList.hidden,true,'Claude 隱藏輸入框上方的摘要');
assert.equal($('providerUsageSummaryPopover').classList.hidden,true);
assert.equal($('providerUsageFiveHourRow').classList.hidden,true);
assert.equal($('providerUsageSevenDayRow').classList.hidden,true);
assert.equal($('providerUsageSummaryDetails').children.length,0);
assert.equal($('providerUsageRateLimits').children.length,2,'側邊欄只留一行說明');
assert.equal($('providerUsageRateLimits').children[0].textContent,'Claude Platform API');
assert.doesNotMatch($('providerUsageRateLimits').children[1].textContent,/[0-9]|%|tokens/,'說明不含任何額度數字');
assert.equal(state.providerUsageSnapshot.rate_limits.length,4,'原始快照仍保留內部數據');
state.providerUsageIdentity=ctx.providerUsageIdentity();
ctx.syncProviderUsageSummary();
assert.equal($('providerUsageSummaryControl').classList.hidden,true,'重新同步不可把 Claude 的摘要又顯示出來');
ctx.renderProviderUsage(quota);
assert.equal($('providerUsageSummaryControl').classList.hidden,false,'切回 Codex 即恢復顯示');
assert.equal($('providerUsageFiveHourRow').classList.hidden,false);
assert.equal($('providerUsageSummaryValue').textContent,'25%');
assert.equal($('providerUsageRateLimits').children.length,0,'Codex 不帶 Claude 的說明');
assert.equal($('providerUsageSummaryDetails').children.length,4);
assert.equal($('providerUsageSummaryDetails').children[1].children[0].children[0].children[0].style.width, '80%');
assert.equal($('providerUsageSummaryDetails').children[3].children[0].children[0].children[0].style.width, '25%');
assert.equal($('providerUsageSummaryDetails').children[1].children[1].textContent, `更新時間 ${ctx.formatProviderUsageReset(updated_at, true)}`);
for (const timestamp of [undefined, 'invalid']) {
  ctx.renderProviderUsage({...quota, updated_at: timestamp});
  assert.equal($('providerUsageSummaryDetails').children[1].children.length, 1, '缺少有效更新時間時，不以重設時間代替');
}
ctx.syncProviderUsageSummary();
assert.equal($('providerUsageSummaryControl').classList.hidden,false);
state.session=null;
ctx.renderProviderUsage(quota);
assert.equal($('providerUsageSummaryControl').classList.hidden,true,'沒有 Session 時一律隱藏');
state.session={id:'s'};
state.providerUsageIdentity='';
const html = fs.readFileSync(path.join(__dirname, '../src/web/console/assets/index.html'), 'utf8');
assert.doesNotMatch(html, /id="providerUsage(?:Summary)?Note"/, '移除明細下方說明');
(async()=>{
  const first=ctx.loadActiveProviderUsage();
  await ctx.loadActiveProviderUsage();assert.equal(pending.length,1,'同一 Provider 不重疊讀取');
  provider='claude';model='model-b';
  const second=ctx.loadActiveProviderUsage();
  pending[1]({...rate,model});await second;
  pending[0](quota);await first;
  assert.equal($('providerUsageSummaryControl').classList.hidden,true,'切到 Claude 後摘要隱藏');
  assert.equal(state.providerUsageSnapshot.kind,'rate_limit','舊 Provider 回覆不可蓋掉新值');
  ctx.renderProviderUsage(quota);
  ctx.closeProviderUsagePopover();
  ctx.toggleProviderUsageSummary();assert.equal($('providerUsageSummaryButton').attrs['aria-expanded'],'true');
  ctx.closeProviderUsagePopover();assert.equal($('providerUsageSummaryButton').attrs['aria-expanded'],'false');
  pending[2](quota);
  console.log('PASS: Codex 百分比與明細、Claude 不顯示用量、未知／過期、切換競態與開關');
})().catch(error=>{console.error(error);process.exitCode=1;});
