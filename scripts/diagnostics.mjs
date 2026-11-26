#!/usr/bin/env node
// Aggregates steiner diagnostics for cache, provider and tool behaviour.
// Prints aggregates, never individual records.
//
// Usage:
//   node scripts/diagnostics.mjs <mode> [flags]
//   node scripts/diagnostics.mjs peak <file...> [flags]
//   node scripts/diagnostics.mjs prefix <logfile> [flags]
//
// Modes: cache | provider | tools | coldturns | mutate | tui | peak <file...> | prefix <logfile>
//
// cache/provider/tools read the diagnostics directory (one JSONL file per
// stream: cache.jsonl, provider.jsonl, tool.jsonl, internal.jsonl (no mode
// reads internal.jsonl yet); see
// internal/diagnostics/envelope.go for the record shape). coldturns reads
// two of those streams at once and joins them on time, because attributing a
// cold turn needs both what the cache did (cache.jsonl) and what occupied the
// gap before it (tool.jsonl). prefix instead reads a session log (JSONL, one
// runtime event per line) and looks at type: "api_request" events, because
// prefix divergence needs the message_hashes sequence, which only exists
// there -- it never moves to the diagnostics envelope.
//
// peak reads one or more session logs and groups model_call_finished
// prompt_tokens by run and agent. A session log exists only when logging is
// enabled (--log-file, or logging.enabled with logging.file), which is off by
// default, so peak also accepts STEINER_USAGE_TELEMETRY files. The format is
// detected per line; a telemetry line is kind: "usage" and carries
// prompt_tokens but no agent id (source stands in) and no run id unless
// STEINER_USAGE_TELEMETRY_RUN was set, so lines without a run id are reported
// as unattributed observations and never counted as runs. Telemetry records
// compaction calls too, and its lines carry no call kind, so a telemetry
// compaction call looks like a parent call; session-log model_call_finished
// events cover turn calls only, so the two sources measure different peaks for
// the same run. peak accepts --json and --top <n> only (a non-negative
// integer), and a missing or unreadable input file or any other flag is an
// error.
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
//                          prefix, coldturns or peak mode, which read session
//                          logs or more than one stream.
//   --json                 print aggregates as JSON instead of tables
//   --top N                cap unbounded per-key listings (default 20; tui lists every
//                          message type, costliest first)
//   --min-n N              coldturns only: below this many long-delegation
//                          observations, a model's row prints "insufficient"
//                          instead of a rate (default 10). A soft guard, not a
//                          filter -- n prints on every row regardless, so
//                          adequacy stays a human judgement.
//
// mutate mode reads tool.jsonl and provider.jsonl and reports call/ops
// metrics, message grouping, failure reasons, a feature report and a cause
// taxonomy (see scripts/diagnostics_mutate.mjs). Its extra flags:
//   --baseline-sha <sha>   split windows by whether <sha> is an ancestor of
//                          each record's build_sha (git merge-base). Without
//                          it there is a single "all" window. A "featured"
//                          subset -- calls carrying a failures[].match block --
//                          is always reported.
//   --exclude-run <id>     drop one run_id from every stream
//   --model <prefix>       keep only calls attributed to a matching model
//   --samples N            print up to N raw capture_bodies samples
//   --cause <bucket>       with --samples, keep one taxonomy bucket only
//
// tui mode reads tui.jsonl (internal/tui/frame_stats.go): per message type,
// Update and View count, p50/p95 (histogram bucket upper bounds) and max in
// ms, total CPU ms and share of window wall time, plus a window-level summary
// (busy fraction, overlay-open fraction, peak running sub-agents, transcript
// lines). It supports --since/--until, --sha, --json and --compare <a> <b>.
//
// VOCABULARY
//   cold start   -- a run's first usage-bearing cache record (cold_start: true
//                    on the record). Its warmth (cache_read/prompt) is how much
//                    of a fresh run's very first request was still served from
//                    a prompt cache seeded by an earlier process.
//   cold turn    -- NOT a cold start. Any turn that read nothing from cache
//                    (cache_read_tokens == 0). cold_start latches once per
//                    process (internal/agent/cache_diagnostics.go's
//                    coldStartRecorded), so a run has at most one cold start
//                    but may have many cold turns. coldturns mode counts the
//                    latter; the cold_starts column of cache mode counts the
//                    former. Mixing them up silently measures the wrong thing.
//   no cache reporting -- a model whose records never once show
//                    cache_read_tokens > 0. That is indistinguishable from
//                    "every turn missed" unless handled, so coldturns drops
//                    such a model from every denominator and lists it
//                    separately rather than reporting it as 100% cold.
//   cached/req, uncached/req -- decompose the hit-rate ratio the same way
//                    internal/usagestats/report.go's Row.CachedPerRequest /
//                    Row.UncachedPerRequest do, so this script and the Go
//                    /token-stats surface agree operationally:
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

import { readFileSync, existsSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";
import { runMutate } from "./diagnostics_mutate.mjs";
import { runTui, runTuiCompare } from "./diagnostics_tui.mjs";

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
const MIN_N = Number.parseInt(argOf(flagArgs, "--min-n", "10"), 10);
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
// the Go /token-stats surface agree on what "cached" means.
function endpointSwitchCount(records) {
	const conversations = groupBy(records.filter((r) => r.run_id && r.payload?.upstream_endpoint), (r) => `${r.run_id}/${r.source ?? ""}/${r.agent_id ?? ""}`);
	let switches = 0;
	for (const calls of conversations.values()) {
		calls.sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
		for (let i = 1; i < calls.length; i++) {
			if (calls[i - 1].payload.upstream_endpoint !== calls[i].payload.upstream_endpoint) switches++;
		}
	}
	return switches;
}

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
		callKinds: Object.fromEntries([...groupBy(records, (r) => r.payload?.call_kind || "turn")].map(([kind, xs]) => [kind, xs.length])),
		endpoints: Object.fromEntries([...groupBy(records, (r) => r.payload?.upstream_endpoint || "(unknown)")].map(([endpoint, xs]) => [endpoint, xs.length])),
		endpointSwitches: endpointSwitchCount(records),
		coldWarmth: coldWarmthN > 0 ? coldWarmthSum / coldWarmthN : 0,
	};
}

const CACHE_COLUMNS = [
	{ name: "hit_rate", value: (r) => r.metrics.hitRate, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
	{ name: "cached/req", value: (r) => r.metrics.cachedPerReq, fmt: (v) => v.toFixed(0), fmtDelta: (d) => d.toFixed(0) },
	{ name: "uncached/req", value: (r) => r.metrics.uncachedPerReq, fmt: (v) => v.toFixed(0), fmtDelta: (d) => d.toFixed(0) },
	{ name: "cold_starts", value: (r) => r.metrics.coldStarts, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "cold_warmth", value: (r) => r.metrics.coldWarmth, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
	{ name: "call_kinds", value: (r) => Object.entries(r.metrics.callKinds).map(([kind, count]) => `${kind}:${count}`).join(","), fmt: (v) => v || "-", fmtDelta: () => "-" },
	{ name: "endpoints", value: (r) => Object.entries(r.metrics.endpoints).map(([endpoint, count]) => `${endpoint}:${count}`).join(","), fmt: (v) => v || "-", fmtDelta: () => "-" },
	{ name: "endpoint_switches", value: (r) => r.metrics.endpointSwitches, fmt: (v) => String(v), fmtDelta: () => "-" },
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

// ---------------------------------------------------------- coldturns mode

// DELEGATION_TOOLS are the tools whose execution blocks the parent's turn.
// A delegated call is a tool call inside the parent's turn
// (internal/agent/turn_progression.go, advance -> executeToolCalls), so while
// one runs the parent issues no model call and its prompt cache entry sits
// idle. internal/delegation/agent_type.go's IsDelegationTool covers sub_agent
// and follow_up; advisor is included here because it blocks the parent turn
// identically, even though it is not a delegation tool.
const DELEGATION_TOOLS = new Set(["sub_agent", "follow_up", "advisor"]);

// JOIN_SLACK_MS widens a tool call's window when testing whether it fits in
// the gap between two parent turns. Both records are stamped by
// diagnostics.Writer at write time (internal/diagnostics/writer.go), the tool
// one after execution returns and the cache one after the next response is
// parsed, so the two differ by however long the surrounding bookkeeping took.
const JOIN_SLACK_MS = 2000;

// LONG_DELEGATION_MS is the duration above which a delegated call is expected
// to outlive Codex's measured ~5 minute idle cache TTL (docs/user/cache-stats.md).
// Four minutes, not five: the ladder that found the TTL survived a 4 minute
// gap and was gone by 5, so 4 is the last duration known to be safe.
const LONG_DELEGATION_MS = 240_000;

// Bin edges in seconds, upper bound exclusive. Reported as ladders rather
// than averages because the claim under test is a transition ("cache dies
// past N minutes"), and an average smears the transition away.
const DELEGATION_BINS = [[0, 60], [60, 240], [240, 300], [300, 600], [600, Infinity]];
const IDLE_BINS = [[0, 120], [120, 300], [300, 600], [600, Infinity]];

const binLabel = ([lo, hi]) => (hi === Infinity ? `${lo}s+` : `${lo}-${hi}s`);
const binOf = (bins, seconds) => bins.find(([lo, hi]) => seconds >= lo && seconds < hi) ?? null;

const isColdTurn = (record) => (record.payload?.cache_read_tokens ?? 0) === 0;

// uncachedTokens mirrors internal/provider/request_payload.go's
// NonCachedPromptTokens: what this request paid full price for.
const uncachedTokens = (record) => {
	const p = record.payload ?? {};
	return Math.max((p.prompt_tokens ?? 0) - (p.cache_read_tokens ?? 0) - (p.cache_create_tokens ?? 0), 0);
};

// modelsWithoutCacheReporting returns the backend_model_ids that never once
// reported a nonzero cache_read_tokens. Every record is considered, not just
// parent turns: one sub-agent read is enough to prove the field is wired up.
// Such a model is dropped from the cold-turn denominators entirely -- a
// backend that omits prompt_tokens_details.cached_tokens (the openai_compat
// path, internal/provider/openai_wire.go) looks exactly like a backend whose
// cache never hits, and reporting it as 100% cold would be a fabricated
// finding rather than a measured one.
function modelsWithoutCacheReporting(cacheRecords) {
	const reported = new Set();
	const seen = new Set();
	for (const r of cacheRecords) {
		const model = r.payload?.backend_model_id;
		if (!model) continue;
		seen.add(model);
		if ((r.payload.cache_read_tokens ?? 0) > 0) reported.add(model);
	}
	return new Set([...seen].filter((m) => !reported.has(m)));
}

// delegationWindows converts delegation-class tool records into absolute
// [start, end] windows. The record's ts is the completion time and
// duration_ms the elapsed time (internal/tool/executor.go's recordDiagnostics
// is called with time.Since(start)), so the call began duration_ms earlier.
function delegationWindows(toolRecords) {
	return toolRecords
		.filter((r) => DELEGATION_TOOLS.has(r.payload?.tool))
		.map((r) => {
			const end = Date.parse(r.ts);
			const ms = r.payload.duration_ms ?? 0;
			return { runID: r.run_id ?? "", tool: r.payload.tool, start: end - ms, end, ms };
		})
		.filter((d) => Number.isFinite(d.start));
}

// orderBySeq orders records by process-global seq (internal/diagnostics's
// writer assigns it), falling back to the order they were read for records
// that predate the field. FileLogSink appends each record serialized, so file
// order is write order; ts is wall-clock and can step backwards, which would
// mis-pair consecutive turns. Consecutive turns are what this join walks, so
// the pairing must follow seq rather than ts.
function orderBySeq(records) {
	return records
		.map((r, i) => ({ r, i }))
		.sort((a, b) => {
			if (Number.isFinite(a.r.seq) && Number.isFinite(b.r.seq)) return a.r.seq - b.r.seq;
			return a.i - b.i;
		})
		.map((x) => x.r);
}

// parentTurnPairs walks consecutive parent turns within a run and describes
// the gap between them: how long it was, and the longest delegation-class
// call that fits inside it. The first turn of each run is deliberately
// dropped -- it has no predecessor, so it is a cold start rather than a cold
// turn, and counting it would attribute every session launch to whatever
// happened to run next.
function parentTurnPairs(cacheRecords, toolRecords) {
	const delegations = delegationWindows(toolRecords);
	const parents = cacheRecords.filter((r) => r.source === "parent" && Number.isFinite(Date.parse(r.ts)));
	const pairs = [];
	for (const [runID, records] of groupBy(parents, (r) => r.run_id ?? "")) {
		const sorted = orderBySeq(records);
		for (let i = 1; i < sorted.length; i++) {
			const prevTs = Date.parse(sorted[i - 1].ts);
			const curTs = Date.parse(sorted[i].ts);
			let longest = null;
			for (const d of delegations) {
				if (d.runID !== runID) continue;
				if (d.start < prevTs - JOIN_SLACK_MS || d.end > curTs + JOIN_SLACK_MS) continue;
				if (!longest || d.ms > longest.ms) longest = d;
			}
			pairs.push({
				record: sorted[i],
				model: sorted[i].payload?.backend_model_id ?? "(unknown)",
				cold: isColdTurn(sorted[i]),
				gapSeconds: (curTs - prevTs) / 1000,
				delegationSeconds: longest ? longest.ms / 1000 : 0,
				delegationTool: longest?.tool ?? null,
				sharedPrefixMessages: sorted[i].payload?.shared_prefix_messages ?? 0,
				// Left undefined when the record predates the fields, so
				// classifyColdTurn can tell "explicitly no baseline" from "old
				// record that never said" and from "comparison was disabled".
				prefixPredecessorKnown: sorted[i].payload?.prefix_predecessor_known,
				prefixComparisonEnabled: sorted[i].payload?.prefix_comparison_enabled,
				// The producer sets this on the first parent record after an
				// interactive resume whose comparison found no predecessor.
				resumeBaselineUnavailable: sorted[i].payload?.resume_baseline_unavailable === true,
				uncached: uncachedTokens(sorted[i]),
			});
		}
	}
	return pairs;
}

// classifyColdTurn attributes one cold turn to a cause.
//
// no_prior_baseline is tested first and needs both producer flags: an
// explicit prefix_comparison_enabled: true (the producer actually ran a
// comparison) and prefix_predecessor_known: false (it found no in-process
// predecessor to compare against). A disabled or absent comparison flag means
// no comparison signal at all, so it must fall through to the legacy reading
// below rather than masquerade as a missing baseline. Records written before
// either field existed omit both and keep that legacy reading too.
//
// prefix_rewrite is tested next and wins over delegation: when the prefix
// diverges at message 0 the request cannot hit any cached entry no matter how
// recently it was touched, so timing explains nothing extra. #569 asks for
// compaction to be counted separately rather than folded into delegation, and
// this is that separation.
//
// The prefix signal is deliberately narrow. cache.jsonl carries
// shared_prefix_messages but no message count to compare it against, so only
// a total divergence (0, or the field absent) is detectable here. A partial
// rewrite -- the common case for compaction, which keeps a summarised head --
// falls through to idle or unexplained. Use prefix mode on a session log for
// the full picture; it has the message_hashes sequence this stream lacks.
function classifyColdTurn(pair) {
	if (pair.prefixComparisonEnabled === true && pair.prefixPredecessorKnown === false) return "no_prior_baseline";
	if (pair.sharedPrefixMessages === 0) return "prefix_rewrite";
	if (pair.delegationSeconds * 1000 >= LONG_DELEGATION_MS) return "delegation";
	if (pair.gapSeconds * 1000 >= LONG_DELEGATION_MS) return "idle";
	return "unexplained";
}

function coldTurnMetrics(pairs, minN) {
	const cold = pairs.filter((p) => p.cold);
	const longDelegation = pairs.filter((p) => p.delegationSeconds * 1000 >= LONG_DELEGATION_MS);
	return {
		n: pairs.length,
		coldTurns: cold.length,
		coldRate: pairs.length ? cold.length / pairs.length : 0,
		uncachedOnCold: sum(cold.map((p) => p.uncached)),
		longDelegationN: longDelegation.length,
		longDelegationCold: longDelegation.filter((p) => p.cold).length,
		adequate: longDelegation.length >= minN,
	};
}

const COLDTURN_COLUMNS = [
	{ name: "cold_turns", value: (r) => r.metrics.coldTurns, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "cold_rate", value: (r) => r.metrics.coldRate, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
	{ name: "uncached_on_cold", value: (r) => r.metrics.uncachedOnCold, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "long_deleg_n", value: (r) => r.metrics.longDelegationN, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	// Printed as "insufficient" rather than 0/N when the sample is too thin:
	// a bare "0%" from three observations reads like a result and is not one.
	{
		name: "long_deleg_cold",
		value: (r) => (r.metrics.adequate ? r.metrics.longDelegationCold : null),
		fmt: (v) => (v === null ? "insufficient" : String(v)),
		fmtDelta: (d) => d.toFixed(0),
	},
];

// coldTurnModelRows groups by backend_model_id and never by provider_type.
// Several models can share one provider_type while being served by entirely
// different upstreams -- every non-codex model in a typical config routes
// through one reseller provider and surfaces as "openai_compat" -- so
// grouping by provider would average unrelated cache implementations
// together and hide the variation this mode exists to expose.
function coldTurnModelRows(pairs, minN) {
	const grouped = sortedByCount(groupBy(pairs, (p) => p.model));
	return capTop(grouped).map(([key, xs]) => ({ key, n: xs.length, metrics: coldTurnMetrics(xs, minN) }));
}

// baselineUnavailableAfterResumeRows counts records flagged by the producer as
// the first no-predecessor request after an interactive resume. Prefix
// baselines are process-local, so a resumed session starts without one. The
// label reports missing diagnostic context only: it does not say the resume
// caused a cache miss, so these turns are kept out of the attribution table and
// listed here instead. A CLI resume starts a new run, so its first record is
// also the run's first turn and never becomes a pair; counting the records
// themselves keeps that case visible.
function baselineUnavailableAfterResumeRows(cacheRecords) {
	const flagged = cacheRecords.filter((r) => r.payload?.resume_baseline_unavailable === true);
	return capTop(sortedByCount(groupBy(flagged, (r) => r.payload?.backend_model_id ?? "(unknown)"))).map(([key, xs]) => ({
		key,
		n: xs.length,
		metrics: {},
	}));
}

function attributionRows(pairs) {
	const cold = pairs.filter((p) => p.cold);
	const grouped = groupBy(cold, classifyColdTurn);
	return [...grouped.entries()]
		.sort((a, b) => b[1].length - a[1].length)
		.map(([key, xs]) => ({
			key,
			n: xs.length,
			metrics: { pct: cold.length ? (100 * xs.length) / cold.length : 0, uncached: sum(xs.map((p) => p.uncached)) },
		}));
}

const ATTRIBUTION_COLUMNS = [
	{ name: "pct", value: (r) => r.metrics.pct, fmt: (v) => v.toFixed(1) + "%", fmtDelta: (d) => d.toFixed(1) + "pp" },
	{ name: "uncached", value: (r) => r.metrics.uncached, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
];

// ladderRows keys rows as "model | bin" so per-model detail survives, and
// emits bins in edge order rather than by count: a ladder read out of order
// cannot show a transition.
function ladderRows(pairs, bins, secondsOf) {
	const rows = [];
	for (const [model, xs] of groupBy(pairs, (p) => p.model)) {
		for (const bin of bins) {
			const inBin = xs.filter((p) => binOf(bins, secondsOf(p)) === bin);
			if (inBin.length === 0) continue;
			const cold = inBin.filter((p) => p.cold).length;
			rows.push({
				key: `${model} | ${binLabel(bin)}`,
				n: inBin.length,
				metrics: { cold, coldRate: cold / inBin.length },
			});
		}
	}
	return capTop(rows);
}

const LADDER_COLUMNS = [
	{ name: "cold", value: (r) => r.metrics.cold, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) },
	{ name: "cold_rate", value: (r) => r.metrics.coldRate, fmt: (v) => (v * 100).toFixed(1) + "%", fmtDelta: (d) => (d * 100).toFixed(1) + "pp" },
];

function excludedModelRows(cacheRecords, excluded) {
	return [...excluded]
		.map((model) => ({
			key: model,
			n: cacheRecords.filter((r) => r.payload?.backend_model_id === model).length,
			metrics: {},
		}))
		.sort((a, b) => b.n - a.n);
}

function runColdTurns(cacheRecords, toolRecords) {
	cacheRecords = cacheRecords.filter((r) => (r.payload?.call_kind ?? "") !== "compaction");
	const excluded = modelsWithoutCacheReporting(cacheRecords);
	const included = cacheRecords.filter((r) => !excluded.has(r.payload?.backend_model_id));
	const pairs = parentTurnPairs(included, toolRecords);
	// Resume-flagged pairs have no baseline to compare, so every pair-based
	// table excludes them and their count is reported separately. Keeping them
	// in one table and out of another would report two different cold-turn
	// populations for the same model.
	const attributable = pairs.filter((p) => !p.resumeBaselineUnavailable);

	const sections = {
		by_model: coldTurnModelRows(attributable, MIN_N),
		cold_turn_attribution: attributionRows(attributable),
		baseline_unavailable_after_resume: baselineUnavailableAfterResumeRows(cacheRecords),
		delegation_ladder: ladderRows(attributable, DELEGATION_BINS, (p) => p.delegationSeconds),
		idle_ladder: ladderRows(attributable, IDLE_BINS, (p) => p.gapSeconds),
		excluded_no_cache_reporting: excludedModelRows(cacheRecords, excluded),
	};

	if (AS_JSON) {
		console.log(JSON.stringify(sections, null, 1));
		return;
	}
	printTable("coldturns by model", sections.by_model, COLDTURN_COLUMNS);
	printTable("coldturns cold-turn attribution", sections.cold_turn_attribution, ATTRIBUTION_COLUMNS);
	printTable("coldturns baseline unavailable after resume", sections.baseline_unavailable_after_resume, []);
	printTable("coldturns delegation-duration ladder", sections.delegation_ladder, LADDER_COLUMNS);
	printTable("coldturns idle-gap ladder", sections.idle_ladder, LADDER_COLUMNS);
	printTable("coldturns excluded (no cache reporting)", sections.excluded_no_cache_reporting, []);
}

// --------------------------------------------------------------- peak mode

// PEAK_BUCKETS are the main-agent peak prompt sizes the summary counts runs
// at or above. 200k and 1M are the context windows the compaction threshold
// experiment compares; 500k sits between them.
const PEAK_BUCKETS = [
	{ label: ">= 200k", min: 200_000 },
	{ label: ">= 500k", min: 500_000 },
	{ label: ">= 1M", min: 1_000_000 },
];

// MAIN_AGENT is the group name for the parent agent: session log events leave
// scope.agent_id empty for the parent, and telemetry labels the parent
// source: "parent".
const MAIN_AGENT = "main";

// peakFiles separates file arguments from flags. peak accepts --json and
// --top only: any other flag is an error rather than a silent no-op, so a
// mistyped filter cannot look like a narrower result.
const PEAK_FLAGS_WITH_VALUE = new Set(["--top"]);

function peakFiles(args) {
	const files = [];
	for (let i = 0; i < args.length; i++) {
		const arg = args[i];
		if (arg === "--json") continue;
		if (PEAK_FLAGS_WITH_VALUE.has(arg)) {
			if (!/^\d+$/.test(args[i + 1] ?? "")) return { error: `${arg} requires a non-negative integer` };
			i++;
			continue;
		}
		if (arg.startsWith("-")) return { error: `does not accept ${arg}; it takes --json and --top <n> only` };
		files.push(arg);
	}
	return { files };
}

// isPeakRecord reports whether a parsed line has the shape peak reads: a JSON
// object with a string top-level `type` (session log) or `kind` (telemetry).
// Anything else parses but is not a record, so a minified raw.json, `null`, or
// an array line cannot pass as a JSONL input.
function isPeakRecord(value) {
	return (
		typeof value === "object" &&
		value !== null &&
		!Array.isArray(value) &&
		(typeof value.type === "string" || typeof value.kind === "string")
	);
}

// readPeakJsonl reads one peak input file and counts the lines that are not
// JSONL records. Blank lines are neither records nor malformed: they separate
// records. The malformed count is reported so a partly corrupt input stays
// visible instead of quietly shrinking the sample.
function readPeakJsonl(path) {
	const records = [];
	let malformed = 0;
	for (const line of readFileSync(path, "utf8").split("\n")) {
		const trimmed = line.trim();
		if (!trimmed) continue;
		let value;
		try {
			value = JSON.parse(trimmed);
		} catch {
			malformed++;
			continue;
		}
		if (isPeakRecord(value)) records.push(value);
		else malformed++;
	}
	return { records, malformed };
}

// peakInput checks every input is a readable file and reads its lines. Read
// and stat failures become errors instead of stack traces: a bad path and an
// unreadable one both mean the input was not usable. Only the read itself is
// caught; iteration must not throw, and spreading a large file's records into
// push would hit the engine's argument limit and masquerade as a read error.
// An empty file is a valid input (a rotated generation can hold no records),
// but a file with content and no JSONL record at all is a mistyped path, such
// as a raw stream or a plain JSON file, and is rejected by name.
function peakInput(files) {
	const lines = [];
	let malformed = 0;
	for (const file of files) {
		let stats;
		try {
			stats = statSync(file, { throwIfNoEntry: false });
		} catch (err) {
			return { error: `cannot stat ${file}: ${err.code ?? err.message}` };
		}
		if (!stats) return { error: `no such file: ${file}` };
		if (!stats.isFile()) return { error: `not a regular file: ${file}` };
		let read;
		try {
			read = readPeakJsonl(file);
		} catch (err) {
			return { error: `cannot read ${file}: ${err.code ?? err.message}` };
		}
		if (read.records.length === 0 && stats.size > 0) {
			return { error: `${file} has content but no JSONL records; peak reads session logs and telemetry files` };
		}
		malformed += read.malformed;
		for (const record of read.records) lines.push(record);
	}
	return { lines, malformed };
}

// peakObservation converts one input line to {runID, agent, tokens}, or null
// for lines peak does not measure. Session log events carry a top-level
// run_id and a scope.agent_id; telemetry usage lines carry neither an agent
// id nor, without STEINER_USAGE_TELEMETRY_RUN, a run id.
function peakObservation(line) {
	if (line?.type === "model_call_finished") {
		const tokens = line.payload?.prompt_tokens;
		if (!Number.isFinite(tokens)) return null;
		return { runID: line.run_id ?? "", agent: line.scope?.agent_id || MAIN_AGENT, tokens };
	}
	if (line?.kind === "usage") {
		if (!Number.isFinite(line.prompt_tokens)) return null;
		const source = line.source ?? "";
		return { runID: line.run_id ?? "", agent: source === "" || source === "parent" ? MAIN_AGENT : source, tokens: line.prompt_tokens };
	}
	return null;
}

function peakGroups(observations) {
	const groups = new Map();
	for (const o of observations) {
		const id = `${o.runID}/${o.agent}`;
		const group = groups.get(id) ?? { id, runID: o.runID, agent: o.agent, calls: 0, peak: 0 };
		group.calls++;
		group.peak = Math.max(group.peak, o.tokens);
		groups.set(id, group);
	}
	return [...groups.values()].sort((a, b) => b.peak - a.peak || a.id.localeCompare(b.id));
}

// peakRow labels a group for display. An empty run id belongs to telemetry
// written without STEINER_USAGE_TELEMETRY_RUN: those lines cannot be told
// apart by run, so they are reported as unattributed observations.
const peakRow = (g) => ({ key: `${g.runID || "(unattributed)"}/${g.agent}`, n: g.calls, metrics: { peakPromptTokens: g.peak } });

// peakSummary counts attributed runs and the runs whose main-agent peak
// reaches each bucket. A run whose only measured calls are sub-agent ones
// still counts as a run, but contributes no main-agent peak. Observations
// without a run id are not runs: they are counted separately, reported as a
// summary row and as unattributed rows, and stay out of every bucket.
function peakSummary(groups) {
	const attributed = groups.filter((g) => g.runID !== "");
	const unattributedObservations = groups.filter((g) => g.runID === "").reduce((n, g) => n + g.calls, 0);
	const runs = new Set(attributed.map((g) => g.runID));
	const mainPeaks = new Map();
	for (const g of attributed) {
		if (g.agent !== MAIN_AGENT) continue;
		mainPeaks.set(g.runID, Math.max(mainPeaks.get(g.runID) ?? 0, g.peak));
	}
	const atLeast = {};
	const rows = [{ key: "runs", n: runs.size, metrics: { share: runs.size ? 1 : 0 } }];
	for (const bucket of PEAK_BUCKETS) {
		const count = [...mainPeaks.values()].filter((peak) => peak >= bucket.min).length;
		atLeast[String(bucket.min)] = count;
		rows.push({ key: `main peak ${bucket.label}`, n: count, metrics: { share: runs.size ? count / runs.size : 0 } });
	}
	// No share: these observations are not part of the run denominator.
	rows.push({ key: "excluded unattributed observations", n: unattributedObservations, metrics: { share: null } });
	return { runs: runs.size, atLeast, unattributedObservations, rows };
}

const PEAK_COLUMNS = [{ name: "peak_prompt_tokens", value: (r) => r.metrics.peakPromptTokens, fmt: (v) => String(v), fmtDelta: (d) => d.toFixed(0) }];

const PEAK_SHARE_COLUMNS = [{ name: "share", value: (r) => r.metrics.share, fmt: (v) => (v === null ? "-" : (v * 100).toFixed(1) + "%"), fmtDelta: (d) => (d * 100).toFixed(1) + "pp" }];

function runPeak(files, lines, malformed) {
	const observations = lines.map(peakObservation).filter(Boolean);
	const groups = peakGroups(observations);
	const summary = peakSummary(groups);
	const attributed = capTop(groups.filter((g) => g.runID !== "").map(peakRow));
	const unattributed = capTop(groups.filter((g) => g.runID === "").map(peakRow));
	if (AS_JSON) {
		console.log(
			JSON.stringify(
				{
					files: files.length,
					runs: summary.runs,
					main_peak_at_least: summary.atLeast,
					unattributed_observations: summary.unattributedObservations,
					skipped_malformed_lines: malformed,
					by_run_agent: attributed,
					unattributed,
				},
				null,
				1,
			),
		);
		return;
	}
	if (malformed > 0) console.log(`skipped ${malformed} malformed line${malformed === 1 ? "" : "s"} (not JSONL records)`);
	printTable("peak main-agent threshold counts", summary.rows, PEAK_SHARE_COLUMNS);
	printTable(`peak prompt tokens by run/agent (${files.length} file${files.length === 1 ? "" : "s"})`, attributed, PEAK_COLUMNS);
	printTable("peak unattributed observations (no run_id)", unattributed, PEAK_COLUMNS);
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

// computePrefixRows walks each agent group in session-log order, which is
// append order: FileLogSink serializes writes, so the file reads back in the
// order the runtime issued the requests. It must not sort by payload.turn:
// an interactive prompt starts a fresh turn counter, so turn numbers reset
// within one agent group and sorting by them interleaves unrelated requests.
function computePrefixRows(logfile) {
	const events = readJsonl(logfile).filter((e) => e.type === "api_request" && e.payload?.kind !== "compaction");
	const groups = groupBy(events, (e) => e.scope?.agent_id ?? "");
	const out = [];
	for (const [agentID, evs] of groups) {
		let prevHashes = null;
		for (const ev of evs) {
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
	if (MODE === "peak") {
		if (COMPARE) {
			console.error("--compare does not apply to peak mode, which reads session logs and telemetry files");
			process.exit(1);
		}
		const { files, error } = peakFiles(argv.slice(1));
		if (error) {
			console.error(`peak mode: ${error}`);
			process.exit(1);
		}
		if (files.length === 0) {
			console.error("usage: node scripts/diagnostics.mjs peak <file...> [--json] [--top N]");
			console.error("       <file> is a session log (JSONL with model_call_finished events) or a");
			console.error("       STEINER_USAGE_TELEMETRY file; the format is detected per line, so");
			console.error("       rotated generations and both formats may be passed together.");
			console.error("       A session log exists only with --log-file or logging.enabled, and every");
			console.error("       input file must exist, and a file with content but no JSONL record");
			console.error("       at all is rejected rather than read as an empty experiment.");
			console.error("       Telemetry lines carry no agent id (source stands");
			console.error("       in) and, without STEINER_USAGE_TELEMETRY_RUN, no run id: those lines are");
			console.error("       reported as unattributed observations, never as runs. Telemetry also");
			console.error("       records compaction calls that session logs omit, so their peaks differ.");
			process.exit(1);
		}
		const input = peakInput(files);
		if (input.error) {
			console.error(`peak mode: ${input.error}`);
			process.exit(1);
		}
		runPeak(files, input.lines, input.malformed);
		return;
	}

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

	if (MODE === "coldturns") {
		if (COMPARE) {
			console.error("--compare does not apply to coldturns mode, which joins two streams");
			process.exit(1);
		}
		runColdTurns(filterRecords(loadEnvelope("cache")), filterRecords(loadEnvelope("tool")));
		return;
	}

	if (MODE === "mutate") {
		if (COMPARE) {
			console.error("--compare does not apply to mutate mode; use --baseline-sha for a before/after split");
			process.exit(1);
		}
		runMutate({
			toolRecords: filterRecords(loadEnvelope("tool")),
			providerRecords: filterRecords(loadEnvelope("provider")),
			args: flagArgs,
			asJson: AS_JSON,
			top: TOP,
			printTable,
		});
		return;
	}

	if (MODE === "tui") {
		const helpers = { groupBy, sortedByCount, capTop };
		const all = loadEnvelope("tui");
		if (COMPARE) {
			runTuiCompare({
				recordsA: filterRecords(all, COMPARE[0]),
				recordsB: filterRecords(all, COMPARE[1]),
				helpers,
				emitCompareSections,
			});
			return;
		}
		runTui({ records: filterRecords(all), helpers, printTable, asJson: AS_JSON });
		return;
	}

	if (!["cache", "provider", "tools"].includes(MODE)) {
		console.error("usage: node scripts/diagnostics.mjs <cache|provider|tools|coldturns|mutate|tui|peak> [flags]");
		console.error("       node scripts/diagnostics.mjs peak <file...> [flags]");
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
