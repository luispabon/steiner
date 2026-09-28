// Fleet-wide classification of uncached input in steiner cache.jsonl.
// Buckets per request i>1 in a conversation:
//   growth      : max(0, P_i - P_{i-1})   intrinsic new content
//   granularity : lost <= GRAN            provider block rounding (intrinsic)
//   rewrite     : lost > GRAN and (prompt shrank >3% or shared_prefix_messages failed to grow)
//   routing     : lost > GRAN on a pure append (provider-side eviction / upstream switch)
// plus first-turn uncached (cold start of each conversation).
import fs from 'node:fs';
const [file, since] = process.argv.slice(2);
const GRAN = 2048;
const recs = fs.readFileSync(file, 'utf8').split('\n').filter(Boolean).map(l => { try { return JSON.parse(l); } catch { return null; } }).filter(Boolean).filter(r => !since || r.ts >= since);
const convs = new Map();
for (const r of recs) {
  const k = `${r.run_id}|${r.source}|${r.agent_id || ''}|${r.payload.backend_model_id}`;
  if (!convs.has(k)) convs.set(k, []);
  convs.get(k).push(r);
}
const G = {};
const add = (k, f, v) => { G[k] ||= { P: 0, R: 0, first: 0, growth: 0, gran: 0, rewrite: 0, routing: 0, n: 0, rwN: 0, rtN: 0 }; G[k][f] += v; };
for (const [k, rs] of convs) {
  rs.sort((a, b) => a.seq - b.seq);
  const model = rs[0].payload.backend_model_id, src = rs[0].source;
  const keys = [`${src} ${model}`, `${src} ALL`, 'ALL'];
  for (let i = 0; i < rs.length; i++) {
    const p = rs[i].payload, P = p.prompt_tokens || 0, R = p.cache_read_tokens || 0;
    for (const g of keys) { add(g, 'P', P); add(g, 'R', R); add(g, 'n', 1); }
    if (i === 0) { for (const g of keys) add(g, 'first', P - R); continue; }
    const q = rs[i - 1].payload, P0 = q.prompt_tokens || 0;
    const growth = Math.max(0, P - P0);
    const lost = Math.max(0, Math.min(P0, P) - R);
    const shrank = P < P0 * 0.97;
    const noGrow = p.shared_prefix_messages === undefined || (q.shared_prefix_messages !== undefined && p.shared_prefix_messages <= q.shared_prefix_messages);
    // uncached this request = P - R = growth + lost (approximately; if P<P0 growth=0)
    for (const g of keys) {
      add(g, 'growth', Math.min(growth, P - R));
      if (lost <= GRAN) add(g, 'gran', lost);
      else if (shrank || noGrow) { add(g, 'rewrite', lost); add(g, 'rwN', 1); }
      else { add(g, 'routing', lost); add(g, 'rtN', 1); }
    }
  }
}
const pct = (a, b) => (100 * a / b).toFixed(1) + '%';
console.log('group'.padEnd(34), 'reqs'.padStart(6), 'hit'.padStart(6), '| share of UNCACHED input:', 'first-turn', 'growth', 'granularity', 'rewrite(n)', 'routing(n)', '| hit if no rewrite+routing');
for (const [k, g] of Object.entries(G).sort((a, b) => b[1].P - a[1].P)) {
  if (g.n < 50) continue;
  const U = g.P - g.R;
  const ideal = (g.R + g.rewrite + g.routing) / g.P;
  console.log(k.padEnd(34), String(g.n).padStart(6), pct(g.R, g.P).padStart(6), '|', pct(g.first, U).padStart(9), pct(g.growth, U).padStart(7), pct(g.gran, U).padStart(8),
    `${pct(g.rewrite, U)}(${g.rwN})`.padStart(13), `${pct(g.routing, U)}(${g.rtN})`.padStart(13), '|', pct(ideal * g.P, g.P));
}
