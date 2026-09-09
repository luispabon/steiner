#!/usr/bin/env node
// Aggregates steiner diagnostics for cache, provider and tool behaviour.
// Prints aggregates, never individual records.
//
// Usage:
//   node scripts/diagnostics.mjs <mode> [flags]
//   node scripts/diagnostics.mjs prefix <logfile> [flags]
//
// Modes: cache | provider | tools | prefix <logfile>
//
// cache/provider/tools read the diagnostics directory (one JSONL file per
// stream: cache.jsonl, provider.jsonl, tool.jsonl; see
// internal/diagnostics/envelope.go for the record shape). prefix instead
// reads a session log (JSONL, one runtime event per line) and looks at
// type: "api_request" events, because prefix divergence needs the
// message_hashes sequence, which only exists there -- it never moves to the
// diagnostics envelope.
//
// Flags:
//   --dir <path>          diagnostics directory (default: $XDG_STATE_HOME/steiner/diagnostics,
//                          else ~/.local/state/steiner/diagnostics -- mirrors
//                          internal/config's defaultDiagnosticsDir)
//   --since YYYY-MM-DD     only records with ts >= this date
//   --until YYYY-MM-DD     only records with ts <= this date
//   --sha <build_sha>      only records with this build_sha
//   --compare <a> <b>      print two columns (build_sha a, build_sha b) and a
//                          delta, for every row of the running mode. This is
//                          the before/after operation: every record carries
//                          build_sha for exactly this reason. Not valid with
//                          prefix mode, which is single-file.
//   --json                 print aggregates as JSON instead of tables
//   --top N                cap unbounded per-key listings (default 20)
//
// VOCABULARY
//   cold start   -- a run's first usage-bearing cache record (cold_start: true
//                    on the record). Its warmth (cache_read/prompt) is how much
//                    of a fresh run's very first request was still served from
//                    a prompt cache seeded by an earlier process.
//   cached/req, uncached/req -- decompose the hit-rate ratio the same way
//                    internal/usagestats/report.go's Row.CachedPerRequest /
//                    Row.UncachedPerRequest do, so this script and the Go
//                    /cache-stats surface agree operationally:
//                      nonCached = max(prompt_tokens - cache_read_tokens - cache_create_tokens, 0)
//                      uncached/req = mean(nonCached + cache_create_tokens)
//                      cached/req   = mean(cache_read_tokens)
//                      hit_rate     = sum(cache_read) / sum(nonCached + cache_read + cache_create)
//   APPEND-ONLY / BREAK-AT-N -- prefix mode's verdict on one turn relative to
//                    the previous turn in the same agent group. APPEND-ONLY
//                    means every earlier message survived unchanged and only
//                    new ones were appended (safe for prompt caching);
//                    BREAK-AT-N means the common prefix is only N messages
//                    long, so something before the tail changed.

import { readFileSync, existsSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

const argv = process.argv.slice(2);
const MODE = argv[0];
const flagArgs = argv.slice(MODE === "prefix" ? 2 : 1);

const argOf = (args, name, fallback) => {
	const i = args.indexOf(name);
	return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
};

function defaultDiagnosticsDir() {
	const xdg = process.env.XDG_STATE_HOME;
	if (xdg) return join(xdg, "steiner", "diagnostics");
	return join(homedir(), ".local", "state", "steiner", "diagnostics");
}

const DIR = argOf(flagArgs, "--dir", defaultDiagnosticsDir());
const SINCE = argOf(flagArgs, "--since", null);
const UNTIL = argOf(flagArgs, "--until", null);
const SHA = argOf(flagArgs, "--sha", null);
const AS_JSON = flagArgs.includes("--json");
const TOP = Number.parseInt(argOf(flagArgs, "--top", "20"), 10);
const compareIdx = flagArgs.indexOf("--compare");
const COMPARE = compareIdx >= 0 ? [flagArgs[compareIdx + 1], flagArgs[compareIdx + 2]] : null;

// ------------------------------------------------------------------ reading

function readJsonl(path) {
	if (!existsSync(path)) return [];
	const out = [];
	for (const line of readFileSync(path, "utf8").split("\n")) {
		const trimmed = line.trim();
		if (!trimmed) continue;
		try {
			out.push(JSON.parse(trimmed));
		} catch {
			continue;
		}
	}
	return out;
}

function loadStream(kind) {
	const stem = `${kind}.jsonl`;
	if (!existsSync(DIR)) return [];
	const files = readdirSync(DIR)
		.filter((name) => name === stem || (name.startsWith(`${stem}.`) && /^\d+$/.test(name.slice(stem.length + 1))))
		.sort((a, b) => {
			const generation = (name) => name === stem ? 0 : Number(name.slice(stem.length + 1));
			return generation(b) - generation(a);
		});
	return files.flatMap((name) => readJsonl(join(DIR, name)));
}

function withinWindow(ts) {
	if (!ts) return true;
	const day = String(ts).slice(0, 10);
	if (SINCE && day < SINCE) return false;
	if (UNTIL && day > UNTIL) return false;
	return true;
}

function filterRecords(records, shaOverride) {
	const sha = shaOverride ?? SHA;
	return records.filter((r) => withinWindow(r.ts) && (!sha || r.build_sha === sha));
}

// --------------------------------------------------------------- utilities

const num = (xs) => xs.filter((x) => typeof x === "number" && Number.isFinite(x));
const sum = (xs) => num(xs).reduce((a, b) => a + b, 0);
const mean = (xs) => (num(xs).length ? sum(xs) / num(xs).length : 0);
const pct = (xs, p) => {
	const s = num(xs).sort((a, b) => a - b);
	return s.length ? s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))] : 0;
};

function groupBy(xs, keyFn) {
	const m = new Map();
	for (const x of xs) {
		const k = keyFn(x);
		if (!m.has(k)) m.set(k, []);
		m.get(k).push(x);
	}
	return m;
}

function sortedByCount(m) {
	return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
}

function capTop(entries) {
	return TOP > 0 ? entries.slice(0, TOP) : entries;
}

function printTable(title, rows, columns) {
	console.log(`\n== ${title}`);
	if (rows.length === 0) {
		console.log("(no data)");
		return;
	}
	const header = ["key", "n", ...columns.map((c) => c.name)];
	const body = rows.map((r) => [String(r.key), String(r.n), ...columns.map((c) => c.fmt(c.value(r)))]);
	const widths = header.map((h, i) => Math.max(h.length, ...body.map((row) => row[i].length)));
	const line = (r) => r.map((v, i) => v.padEnd(widths[i])).join("  ");
	console.log(line(header));
	console.log(widths.map((w) => "-".repeat(w)).join("  "));
	for (const r of body) console.log(line(r));
}

// buildCompareRows merges two aggregate row sets keyed by `key`, computing
// a raw (a, b, delta) triple per column. Shared by the text and --json
// renderers so they can never disagree about the numbers.
function buildCompareRows(rowsA, rowsB, columns) {
	const keys = [...new Set([...rowsA.map((r) => r.key), ...rowsB.map((r) => r.key)])];
	const byKeyA = new Map(rowsA.map((r) => [r.key, r]));
	const byKeyB = new Map(rowsB.map((r) => [r.key, r]));
	return keys.map((key) => {
		const a = byKeyA.get(key);
		const b = byKeyB.get(key);
		const cols = {};
		for (const c of columns) {
			const va = a ? c.value(a) : null;
			const vb = b ? c.value(b) : null;
			cols[c.name] = { a: va, b: vb, delta: va !== null && vb !== null ? vb - va : null };
		}
		return { key, n_a: a?.n ?? 0, n_b: b?.n ?? 0, ...cols };
	});
}

// emitCompareSections renders every {title, rowsA, rowsB, columns} section
// of a --compare run. In --json mode all sections are collected into one
// object and printed once, so a consumer parses a single JSON document
// instead of one blob per section.
function emitCompareSections(sections) {
	if (AS_JSON) {
		const out = { sha_a: COMPARE[0], sha_b: COMPARE[1], sections: {} };
		for (const s of sections) out.sections[s.title] = buildCompareRows(s.rowsA, s.rowsB, s.columns);
		console.log(JSON.stringify(out, null, 1));
		return;
	}
	for (const s of sections) printCompareTable(s.title, s.rowsA, s.rowsB, s.columns);
}

function printCompareTable(title, rowsA, rowsB, columns) {
	const merged = buildCompareRows(rowsA, rowsB, columns);
	console.log(`\n== ${title} (${COMPARE[0]} vs ${COMPARE[1]})`);
	if (merged.length === 0) {
		console.log("(no data)");
		return;
	}
	const header = ["key", `n(${COMPARE[0]})`, `n(${COMPARE[1]})`];
	for (const c of columns) header.push(`${c.name}(a)`, `${c.name}(b)`, `${c.name}(delta)`);
	const body = merged.map((r) => {
		const row = [String(r.key), String(r.n_a), String(r.n_b)];
		for (const c of columns) {
			const cell = r[c.name];
			row.push(
				cell.a !== null ? c.fmt(cell.a) : "-",
				cell.b !== null ? c.fmt(cell.b) : "-",
				cell.delta !== null ? (cell.delta >= 0 ? "+" : "") + c.fmtDelta(cell.delta) : "-",
			);
		}
		return row;
	});
	const widths = header.map((h, i) => Math.max(h.length, ...body.map((row) => row[i].length)));
	const line = (r) => r.map((v, i) => v.padEnd(widths[i])).join("  ");
	console.log(line(header));
	console.log(widths.map((w) => "-".repeat(w)).join("  "));
	for (const r of body) console.log(line(r));
}

// -------------------------------------------------------------- cache mode

// nonCached/cachedPerReq/uncachedPerReq/hitRate mirror
// internal/usagestats/recorder.go / report.go exactly, so this script and
// the Go /cache-stats surface agree on what "cached" means.
function cacheMetrics(records) {
	let promptSum = 0;
	let cacheReadSum = 0;
	let cacheCreateSum = 0;
	let nonCachedSum = 0;
	let coldStarts = 0;
	let coldWarmthSum = 0;
	let coldWarmthN = 0;
	for (const r of records) {
		const p = r.payload ?? {};
		const prompt = p.prompt_tokens ?? 0;
		const cacheRead = p.cache_read_tokens ?? 0;
		const cacheCreate = p.cache_create_tokens ?? 0;
		const nonCached = Math.max(prompt - cacheRead - cacheCreate, 0);
		promptSum += prompt;
		cacheReadSum += cacheRead;
		cacheCreateSum += cacheCreate;
		nonCachedSum += nonCached;
		if (p.cold_start) {
			coldStarts++;
			if (prompt > 0) {
				coldWarmthSum += cacheRead / prompt;
				coldWarmthN++;
			}
		}
	}
	const n = records.length;
	const total = nonCachedSum + cacheReadSum + cacheCreateSum;
	return {
		n,
		promptSum,
		cacheReadSum,
		cacheCreateSum,
		nonCachedSum,
		hitRate: total > 0 ? cacheReadSum / total : 0,
		cachedPerReq: n > 0 ? cacheReadSum / n : 0,
		uncachedPerReq: n > 0 ? (nonCachedSum + cacheCreateSum) / n : 0,
		coldStarts,
		coldWarmth: coldWarmthN > 0 ? coldWarmthSum / coldWarmthN : 0,
	};
}

const CACHE_COLUMNS = [
	{ name: "hit_rate", value: (r) => r.metrics.hitRate, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
	{ name: "cached/req", value: (r) => r.metrics.cachedPerReq, fmt: (v) => v.toFixed(0), fmtDelta: (d) => d.toFixed(0) },
	{ name: "uncached/req", value: (r) => r.metrics.uncachedPerReq, fmt: (v) => v.toFixed(0), fmtDelta: (d) => d.toFixed(0) },
	{ name: "cold_starts", value: (r) => r.metrics.coldStarts, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "cold_warmth", value: (r) => r.metrics.coldWarmth, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
];

function cacheGroupRows(records, keyFn) {
	const grouped = sortedByCount(groupBy(records, keyFn));
	return capTop(grouped).map(([key, xs]) => ({ key, n: xs.length, metrics: cacheMetrics(xs) }));
}

function runCache(records) {
	const groups = {
		"by source": cacheGroupRows(records, (r) => r.source ?? "(top-level)"),
		"by agent_type": cacheGroupRows(records, (r) => r.agent_type || "(top-level)"),
		"by model": cacheGroupRows(records, (r) => r.payload?.backend_model_id ?? "(unknown)"),
	};
	if (AS_JSON) {
		console.log(JSON.stringify(groups, null, 1));
		return;
	}
	for (const [title, rows] of Object.entries(groups)) {
		printTable(`cache ${title}`, rows, CACHE_COLUMNS);
	}
}

function runCacheCompare(all) {
	const [shaA, shaB] = COMPARE;
	const recordsA = filterRecords(all, shaA);
	const recordsB = filterRecords(all, shaB);
	const dims = {
		"by source": (r) => r.source ?? "(top-level)",
		"by agent_type": (r) => r.agent_type || "(top-level)",
		"by model": (r) => r.payload?.backend_model_id ?? "(unknown)",
	};
	emitCompareSections(
		Object.entries(dims).map(([title, keyFn]) => ({
			title: `cache ${title}`,
			rowsA: cacheGroupRows(recordsA, keyFn),
			rowsB: cacheGroupRows(recordsB, keyFn),
			columns: CACHE_COLUMNS,
		})),
	);
}

// ----------------------------------------------------------- provider mode

function providerMetrics(records) {
	const n = records.length;
	const outcomes = groupBy(records, (r) => r.payload?.outcome ?? "unknown");
	const retried = records.filter((r) => r.payload?.outcome === "retried" || (r.payload?.attempts ?? 1) > 1).length;
	return {
		n,
		outcomes: [...outcomes.entries()].map(([outcome, xs]) => ({ outcome, n: xs.length, pct: n ? (100 * xs.length) / n : 0 })),
		retryRate: n ? retried / n : 0,
		ttftP50: pct(records.map((r) => r.payload?.ttft_ms), 50),
		ttftP95: pct(records.map((r) => r.payload?.ttft_ms), 95),
		durationP50: pct(records.map((r) => r.payload?.duration_ms), 50),
		durationP95: pct(records.map((r) => r.payload?.duration_ms), 95),
	};
}

const PROVIDER_COLUMNS = [
	{ name: "retry_rate", value: (r) => r.metrics.retryRate, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
	{ name: "ttft_p50_ms", value: (r) => r.metrics.ttftP50, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "ttft_p95_ms", value: (r) => r.metrics.ttftP95, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "dur_p50_ms", value: (r) => r.metrics.durationP50, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "dur_p95_ms", value: (r) => r.metrics.durationP95, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
];

function providerGroupRows(records, keyFn) {
	const grouped = sortedByCount(groupBy(records, keyFn));
	return capTop(grouped).map(([key, xs]) => ({ key, n: xs.length, metrics: providerMetrics(xs) }));
}

function outcomeMixRows(records) {
	const n = records.length;
	return capTop(sortedByCount(groupBy(records, (r) => r.payload?.outcome ?? "unknown")).map(([key, xs]) => ({
		key,
		n: xs.length,
		metrics: { pct: n ? (100 * xs.length) / n : 0 },
	})));
}

const OUTCOME_COLUMN = [{ name: "pct", value: (r) => r.metrics.pct, fmt: (v) => v.toFixed(1) + "%", fmtDelta: (d) => d.toFixed(1) + "pp" }];

function errorClassRows(records) {
	const withClass = records.filter((r) => r.payload?.error_class);
	const n = withClass.length;
	return capTop(sortedByCount(groupBy(withClass, (r) => r.payload.error_class)).map(([key, xs]) => ({
		key,
		n: xs.length,
		metrics: { pct: n ? (100 * xs.length) / n : 0 },
	})));
}

function runProvider(records) {
	if (AS_JSON) {
		console.log(
			JSON.stringify(
				{
					outcome_mix: outcomeMixRows(records),
					by_provider_model: providerGroupRows(records, (r) => `${r.payload?.provider ?? "?"}/${r.payload?.model ?? "?"}`),
					error_class: errorClassRows(records),
				},
				null,
				1,
			),
		);
		return;
	}
	printTable("provider outcome mix", outcomeMixRows(records), OUTCOME_COLUMN);
	printTable("provider by provider/model", providerGroupRows(records, (r) => `${r.payload?.provider ?? "?"}/${r.payload?.model ?? "?"}`), PROVIDER_COLUMNS);
	printTable("provider error_class histogram", errorClassRows(records), OUTCOME_COLUMN);
}

function runProviderCompare(all) {
	const [shaA, shaB] = COMPARE;
	const recordsA = filterRecords(all, shaA);
	const recordsB = filterRecords(all, shaB);
	emitCompareSections([
		{ title: "provider outcome mix", rowsA: outcomeMixRows(recordsA), rowsB: outcomeMixRows(recordsB), columns: OUTCOME_COLUMN },
		{
			title: "provider by provider/model",
			rowsA: providerGroupRows(recordsA, (r) => `${r.payload?.provider ?? "?"}/${r.payload?.model ?? "?"}`),
			rowsB: providerGroupRows(recordsB, (r) => `${r.payload?.provider ?? "?"}/${r.payload?.model ?? "?"}`),
			columns: PROVIDER_COLUMNS,
		},
		{ title: "provider error_class histogram", rowsA: errorClassRows(recordsA), rowsB: errorClassRows(recordsB), columns: OUTCOME_COLUMN },
	]);
}

// -------------------------------------------------------------- tool mode

function toolFailed(r) {
	const p = r.payload ?? {};
	return (p.ops_failed ?? 0) > 0 || p.outcome !== "ok";
}

function toolMetrics(records) {
	const n = records.length;
	const failed = records.filter(toolFailed).length;
	return { n, failRate: n ? failed / n : 0 };
}

const TOOL_COLUMNS = [{ name: "fail_rate", value: (r) => r.metrics.failRate, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" }];

function toolGroupRows(records, keyFn) {
	const grouped = sortedByCount(groupBy(records, keyFn));
	return capTop(grouped).map(([key, xs]) => ({ key, n: xs.length, metrics: toolMetrics(xs) }));
}

// reasonHistogramRows groups by tool/reason, not reason alone: a flat
// histogram would put mutate's no_match next to bash's approval_denied with
// no way to tell which tool a reason came from.
function reasonHistogramRows(records) {
	const failures = records.flatMap((r) => (r.payload?.failures ?? []).map((f) => ({ tool: r.payload?.tool ?? "unknown", reason: f.reason ?? "unknown" })));
	const n = failures.length;
	return capTop(sortedByCount(groupBy(failures, (f) => `${f.tool}/${f.reason}`)).map(([key, xs]) => ({
		key,
		n: xs.length,
		metrics: { pct: n ? (100 * xs.length) / n : 0 },
	})));
}

function mutateOpRows(records) {
	const mutate = records.filter((r) => r.payload?.tool === "mutate");
	const failures = mutate.flatMap((r) => r.payload?.failures ?? []);
	const n = failures.length;
	return capTop(sortedByCount(groupBy(failures, (f) => f.op || "unknown")).map(([key, xs]) => ({
		key,
		n: xs.length,
		metrics: { pct: n ? (100 * xs.length) / n : 0 },
	})));
}

function runTools(records) {
	if (AS_JSON) {
		console.log(
			JSON.stringify(
				{
					by_tool: toolGroupRows(records, (r) => r.payload?.tool ?? "unknown"),
					reason_histogram: reasonHistogramRows(records),
					mutate_by_op: mutateOpRows(records),
				},
				null,
				1,
			),
		);
		return;
	}
	printTable("tool failure rate by tool", toolGroupRows(records, (r) => r.payload?.tool ?? "unknown"), TOOL_COLUMNS);
	printTable("tool reason histogram", reasonHistogramRows(records), OUTCOME_COLUMN);
	printTable("mutate failures by op", mutateOpRows(records), OUTCOME_COLUMN);
}

function runToolsCompare(all) {
	const [shaA, shaB] = COMPARE;
	const recordsA = filterRecords(all, shaA);
	const recordsB = filterRecords(all, shaB);
	emitCompareSections([
		{
			title: "tool failure rate by tool",
			rowsA: toolGroupRows(recordsA, (r) => r.payload?.tool ?? "unknown"),
			rowsB: toolGroupRows(recordsB, (r) => r.payload?.tool ?? "unknown"),
			columns: TOOL_COLUMNS,
		},
		{ title: "tool reason histogram", rowsA: reasonHistogramRows(recordsA), rowsB: reasonHistogramRows(recordsB), columns: OUTCOME_COLUMN },
		{ title: "mutate failures by op", rowsA: mutateOpRows(recordsA), rowsB: mutateOpRows(recordsB), columns: OUTCOME_COLUMN },
	]);
}

// ------------------------------------------------------------- prefix mode

// longestCommonPrefixLen mirrors internal/agent/cache_diagnostics.go's
// function of the same name exactly: the index of the first divergence
// between two message_hashes sequences.
function longestCommonPrefixLen(prev, cur) {
	const n = Math.min(prev.length, cur.length);
	let i = 0;
	while (i < n && prev[i] === cur[i]) i++;
	return i;
}

function computePrefixRows(logfile) {
	const events = readJsonl(logfile).filter((e) => e.type === "api_request");
	const groups = groupBy(events, (e) => e.scope?.agent_id ?? "");
	const out = [];
	for (const [agentID, evs] of groups) {
		const sorted = [...evs].sort((a, b) => (a.payload?.turn ?? 0) - (b.payload?.turn ?? 0));
		let prevHashes = null;
		for (const ev of sorted) {
			const p = ev.payload ?? {};
			const hashes = p.message_hashes ?? [];
			let cached = 0;
			let verdict = "-";
			if (prevHashes !== null) {
				cached = longestCommonPrefixLen(prevHashes, hashes);
				verdict = cached === prevHashes.length ? "APPEND-ONLY" : `BREAK-AT-${cached}`;
			}
			out.push({
				agentID: agentID || "(top-level)",
				turn: p.turn ?? 0,
				msgs: p.message_count ?? 0,
				raw: p.prompt_bytes ?? 0,
				cached,
				verdict,
			});
			prevHashes = hashes;
		}
	}
	return out;
}

function runPrefix(logfile) {
	const rows = computePrefixRows(logfile);
	if (AS_JSON) {
		console.log(JSON.stringify(rows, null, 1));
		return;
	}
	console.log(`\n== prefix divergence: ${logfile}`);
	if (rows.length === 0) {
		console.log("(no api_request events found)");
		return;
	}
	const header = ["agent_id", "turn", "msgs", "raw", "cached", "verdict"];
	const body = rows.map((r) => [r.agentID, String(r.turn), String(r.msgs), String(r.raw), String(r.cached), r.verdict]);
	const widths = header.map((h, i) => Math.max(h.length, ...body.map((row) => row[i].length)));
	const line = (r) => r.map((v, i) => v.padEnd(widths[i])).join("  ");
	console.log(line(header));
	console.log(widths.map((w) => "-".repeat(w)).join("  "));
	for (const r of body) console.log(line(r));
}

// ---------------------------------------------------------------- dispatch

function loadEnvelope(kind) {
	return loadStream(kind).map((rec) => ({
		...rec,
		payload: rec.payload ?? {},
	}));
}

function main() {
	if (MODE === "prefix") {
		if (COMPARE) {
			console.error("--compare does not apply to prefix mode, which reads a single session log");
			process.exit(1);
		}
		const logfile = argv[1];
		if (!logfile) {
			console.error("usage: node scripts/diagnostics.mjs prefix <logfile> [flags]");
			process.exit(1);
		}
		runPrefix(logfile);
		return;
	}

	if (!["cache", "provider", "tools"].includes(MODE)) {
		console.error("usage: node scripts/diagnostics.mjs <cache|provider|tools> [flags]");
		console.error("       node scripts/diagnostics.mjs prefix <logfile> [flags]");
		process.exit(1);
	}

	const kind = MODE === "tools" ? "tool" : MODE;
	const all = loadEnvelope(kind);

	if (COMPARE) {
		if (MODE === "cache") runCacheCompare(all);
		else if (MODE === "provider") runProviderCompare(all);
		else runToolsCompare(all);
		return;
	}

	const records = filterRecords(all);
	if (MODE === "cache") runCache(records);
	else if (MODE === "provider") runProvider(records);
	else runTools(records);
}

main();
