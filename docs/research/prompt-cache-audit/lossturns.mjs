// Characterise steiner loss turns: for each turn with lost > 128 tokens,
// record how much was served (R), relative to previous prompt, time gap,
// shared_prefix_messages trend, and whether sibling convs were running concurrently.
import fs from 'node:fs';
const [file, filterModel, since] = process.argv.slice(2);
const recs = fs.readFileSync(file, 'utf8').split('\n').filter(Boolean).map(l => { try { return JSON.parse(l); } catch { return null; } }).filter(Boolean)
  .filter(r => (!since || r.ts >= since) && (!filterModel || r.payload.backend_model_id === filterModel));
const convs = new Map();
for (const r of recs) {
  const key = `${r.run_id}|${r.source}|${r.agent_id || ''}`;
  if (!convs.has(key)) convs.set(key, []);
  convs.get(key).push(r);
}
const hist = { rwDrop: 0, rwNoDrop: 0 };
const buckets = {};
const gapB = {};
let losses = [];
let turns = 0;
for (const [key, rs] of convs) {
  rs.sort((a, b) => a.seq - b.seq);
  const firstR = rs[0].payload.cache_read_tokens || 0;
  for (let i = 1; i < rs.length; i++) {
    const p = rs[i - 1].payload, c = rs[i].payload;
    turns++;
    const P0 = p.prompt_tokens, P1 = c.prompt_tokens, R = c.cache_read_tokens || 0;
    const lost = Math.min(P0, P1) - R;
    if (lost <= 128) continue;
    const gap = (new Date(rs[i].ts) - new Date(rs[i - 1].ts)) / 1000;
    const drop = c.shared_prefix_messages !== undefined && p.shared_prefix_messages !== undefined && c.shared_prefix_messages <= p.shared_prefix_messages;
    const frac = R / Math.min(P0, P1);
    losses.push({ key, i, P0, P1, R, lost, gap, sharedPrev: p.shared_prefix_messages, shared: c.shared_prefix_messages, firstR, frac, ts: rs[i].ts, keyHash: c.cache_key_hash, prevKeyHash: p.cache_key_hash, prefixCmp: c.prefix_comparison_enabled, predKnown: c.prefix_predecessor_known });
    const fb = frac === 0 ? '0' : frac < 0.25 ? '<25%' : frac < 0.5 ? '<50%' : frac < 0.75 ? '<75%' : frac < 0.9 ? '<90%' : '>=90%';
    buckets[fb] = (buckets[fb] || 0) + lost;
    const gb = gap < 60 ? '<1m' : gap < 300 ? '1-5m' : gap < 900 ? '5-15m' : '>15m';
    gapB[gb] = gapB[gb] || { n: 0, lost: 0 }; gapB[gb].n++; gapB[gb].lost += lost;
    if (drop) hist.rwDrop += lost; else hist.rwNoDrop += lost;
  }
}
const total = losses.reduce((a, b) => a + b.lost, 0);
console.log('turns', turns, 'loss turns', losses.length, 'lost tokens', total);
console.log('lost by served-fraction bucket', JSON.stringify(buckets));
console.log('lost by gap', JSON.stringify(gapB));
console.log('lost where shared_prefix did not grow (rewrite-ish)', hist.rwDrop, 'else', hist.rwNoDrop);
// Is R close to the conversation's first-turn cached amount (static prefix only)?
let staticLike = 0; for (const l of losses) if (l.R > 0 && Math.abs(l.R - l.firstR) < 1024) staticLike += l.lost;
console.log('lost where R ~= first-turn cached (static prefix only):', staticLike);
// Distinct R values: provider cache block granularity
console.log('sample losses (largest 25):');
for (const l of losses.sort((a, b) => b.lost - a.lost).slice(0, 25)) console.log(l.ts, l.key.slice(0, 40), 'P0', l.P0, 'P1', l.P1, 'R', l.R, 'gap', l.gap.toFixed(0) + 's', 'shared', l.sharedPrev, '->', l.shared, 'firstR', l.firstR);
// concurrency: at each loss, how many other convs with same model had a request within +-30s
const byTime = recs.map(r => ({ t: +new Date(r.ts), key: `${r.run_id}|${r.source}|${r.agent_id || ''}` }));
function conc(ts, key) { const t = +new Date(ts); const s = new Set(); for (const x of byTime) if (Math.abs(x.t - t) < 30000 && x.key !== key) s.add(x.key); return s.size; }
const cl = {}, ca = {};
for (const l of losses) { const c = conc(l.ts, l.key); const b = c === 0 ? '0' : c < 3 ? '1-2' : c < 6 ? '3-5' : '6+'; cl[b] = (cl[b] || 0) + 1; }
let sample = 0; for (const [key, rs] of convs) for (let i = 1; i < rs.length; i += 5) { const c = conc(rs[i].ts, key); const b = c === 0 ? '0' : c < 3 ? '1-2' : c < 6 ? '3-5' : '6+'; ca[b] = (ca[b] || 0) + 1; sample++; }
console.log('concurrency at loss turns', JSON.stringify(cl), ' vs all turns (sampled 1/5)', JSON.stringify(ca));
