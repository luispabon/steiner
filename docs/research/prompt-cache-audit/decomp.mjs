// Per-request cache decomposition.
// For each conversation (steiner: run_id+agent_id+source; opencode: session_id),
// ordered by seq/time, each request i has prompt P_i and cache read R_i.
//   first-turn: whole prompt of request 1; uncached part = P_1 - R_1
//   growth_i  = max(0, P_i - P_{i-1})           (new content: intrinsic miss)
//   lost_i    = max(0, min(P_{i-1}, P_i) - R_i)  (previously-seen prefix not served from cache)
// lost is split: rewrite (shared_prefix_messages dropped) / cold (R=0) / partial.
import fs from 'node:fs';
import { execFileSync } from 'node:child_process';

const mode = process.argv[2] || 'steiner';
const file = process.argv[3];
const filterModel = process.argv[4];
const since = process.argv[5];
const NOISE = 128;

function convsSteiner() {
  const recs = fs.readFileSync(file, 'utf8').split('\n').filter(Boolean).map(l => { try { return JSON.parse(l); } catch { return null; } }).filter(Boolean);
  const convs = new Map();
  for (const r of recs) {
    if (since && r.ts < since) continue;
    const p = r.payload;
    if (p?.call_kind === 'compaction') continue;
    if (filterModel && !p.backend_model_id.includes(filterModel)) continue;
    const key = `${r.run_id}|${r.source}|${r.agent_id || ''}|${p.backend_model_id}`;
    if (!convs.has(key)) convs.set(key, { model: p.backend_model_id, source: r.source + (r.agent_type ? ':' + r.agent_type : ''), reqs: [] });
    convs.get(key).reqs.push({ seq: r.seq, ts: r.ts, P: p.prompt_tokens || 0, R: p.cache_read_tokens || 0, out: p.completion_tokens || 0, shared: p.shared_prefix_messages, msgs: p.prefix_message_count });
  }
  for (const c of convs.values()) c.reqs.sort((a, b) => a.seq - b.seq);
  return [...convs.values()];
}

function convsOpencode() {
  const py = `
import sqlite3,json,sys
c=sqlite3.connect('file:${file}?mode=ro',uri=True)
out=[]
for sid,t,d in c.execute("select session_id,time_created,data from message order by session_id,time_created"):
  j=json.loads(d)
  if j.get('role')!='assistant' or not j.get('tokens'): continue
  tk=j['tokens'];ca=tk.get('cache',{})
  out.append([sid,t,j.get('modelID'),tk.get('input',0)+ca.get('read',0)+ca.get('write',0),ca.get('read',0),tk.get('output',0)+tk.get('reasoning',0),j.get('parentID') and 1])
print(json.dumps(out))`;
  const rows = JSON.parse(execFileSync('python3', ['-c', py], { maxBuffer: 1 << 28 }).toString());
  const convs = new Map();
  for (const [sid, t, model, P, R, out] of rows) {
    if (filterModel && !String(model).includes(filterModel)) continue;
    if (!P) continue;
    const key = sid + '|' + model;
    if (!convs.has(key)) convs.set(key, { model, source: 'opencode', reqs: [] });
    convs.get(key).reqs.push({ ts: t, P, R, out });
  }
  return [...convs.values()];
}

const convs = mode === 'opencode' ? convsOpencode() : convsSteiner();
const groups = {};
function g(k) {
  return groups[k] ||= { convs: 0, reqs: 0, P: 0, R: 0, firstP: 0, firstR: 0, growth: 0, out: 0, lost: 0, lostRewrite: 0, lostCold: 0, lostPartial: 0, coldTurns: 0, partialTurns: 0, rewriteTurns: 0, lostPerTurn: [] };
}
for (const c of convs) {
  const x = g(c.model + ' ' + c.source.split(':')[0]);
  const all = g('ALL ' + c.model);
  for (const t of [x, all]) {
    t.convs++;
    const f = c.reqs[0];
    t.firstP += f.P; t.firstR += f.R;
  }
  let prev = null;
  for (const r of c.reqs) {
    for (const t of [x, all]) { t.reqs++; t.P += r.P; t.R += r.R; }
    if (prev) {
      const growth = Math.max(0, r.P - prev.P);
      const lost = Math.max(0, Math.min(prev.P, r.P) - r.R);
      for (const t of [x, all]) {
        t.growth += growth; t.out += prev.out;
        if (lost > NOISE) {
          t.lost += lost;
          const rewrite = r.shared !== undefined && prev.msgs !== undefined && r.shared < prev.msgs;
          if (rewrite) { t.lostRewrite += lost; t.rewriteTurns++; }
          else if (r.R === 0) { t.lostCold += lost; t.coldTurns++; }
          else { t.lostPartial += lost; t.partialTurns++; }
        }
      }
    }
    prev = r;
  }
}
const k = n => (n / 1000).toFixed(0) + 'k';
const pct = (a, b) => b ? (100 * a / b).toFixed(1) + '%' : '-';
console.log('group'.padEnd(34), 'convs reqs  hit    ctx/req firstTurnUncached/conv  growth/turn  lost/turn  lost%ofUncached  [rewrite cold partial]turns  prevOut/turn');
for (const [name, t] of Object.entries(groups).sort((a, b) => b[1].P - a[1].P)) {
  if (t.reqs < 20) continue;
  const turns = t.reqs - t.convs;
  const uncached = t.P - t.R;
  console.log(name.padEnd(34), String(t.convs).padStart(5), String(t.reqs).padStart(5), pct(t.R, t.P).padStart(6),
    k(t.P / t.reqs).padStart(7), (k((t.firstP - t.firstR) / t.convs) + ' of ' + k(t.firstP / t.convs)).padStart(14),
    String(Math.round(t.growth / turns)).padStart(10), String(Math.round(t.lost / turns)).padStart(10),
    pct(t.lost, uncached).padStart(10), `[${t.rewriteTurns} ${t.coldTurns} ${t.partialTurns}]/${turns}`.padStart(22),
    'lostSplit(rw/cold/part)=' + [t.lostRewrite, t.lostCold, t.lostPartial].map(k).join('/'),
    'out/turn', Math.round(t.out / turns));
}
