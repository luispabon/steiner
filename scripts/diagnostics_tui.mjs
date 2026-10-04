// tui mode for diagnostics.mjs: per-message-type Update/View cost from the
// tui stream (internal/tui/frame_stats.go). Each record is one message type in
// one 10 s window; histograms use the fixed bucket bounds below, so merging
// windows is a per-bucket sum and percentiles are bucket upper bounds. The max
// column comes from the exact max fields instead. Share is a type's Update +
// View CPU time divided by the wall time of all windows in scope.

// Upper bounds in ms for every bucket but the last (1024 ms and above), which
// has no bound and reports the exact max instead. Must match frameHistBounds.
export const TUI_BUCKET_BOUNDS_MS = [0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512];

function mergeHist(records, field) {
	const merged = new Array(TUI_BUCKET_BOUNDS_MS.length + 1).fill(0);
	for (const r of records) {
		const h = r.payload?.[field];
		if (!Array.isArray(h)) continue;
		h.forEach((c, i) => {
			if (i < merged.length && Number.isFinite(c)) merged[i] += c;
		});
	}
	return merged;
}

function histPercentile(hist, p, maxMs) {
	const total = hist.reduce((a, b) => a + b, 0);
	if (total === 0) return 0;
	const rank = Math.ceil((p / 100) * total);
	let seen = 0;
	for (let i = 0; i < hist.length; i++) {
		seen += hist[i];
		if (seen >= rank) return i < TUI_BUCKET_BOUNDS_MS.length ? TUI_BUCKET_BOUNDS_MS[i] : maxMs;
	}
	return maxMs;
}

const sumOf = (records, field) => records.reduce((a, r) => a + (Number.isFinite(r.payload?.[field]) ? r.payload[field] : 0), 0);
const maxOf = (records, field) => records.reduce((a, r) => Math.max(a, Number.isFinite(r.payload?.[field]) ? r.payload[field] : 0), 0);

// windowsOf keeps one record per (run_id, window_id): window-level fields
// repeat on every message-type record of that window.
function windowsOf(records) {
	const seen = new Map();
	for (const r of records) {
		const key = `${r.run_id ?? ""}|${r.payload?.window_id ?? ""}`;
		if (!seen.has(key)) seen.set(key, r);
	}
	return [...seen.values()];
}

function costMetrics(records, kind, field) {
	const hist = mergeHist(records, `${kind}_hist`);
	const maxMs = maxOf(records, `${kind}_max_ms`);
	return {
		n: sumOf(records, `${kind}_count`),
		p50: histPercentile(hist, 50, maxMs),
		p95: histPercentile(hist, 95, maxMs),
		max: maxMs,
		sum: sumOf(records, `${kind}_sum_ms`),
		field,
	};
}

function typeRows(records, wallMs, { groupBy, sortedByCount, capTop }) {
	return capTop(sortedByCount(groupBy(records, (r) => r.payload?.type ?? "unknown"))).map(([key, xs]) => {
		const update = costMetrics(xs, "update");
		const view = costMetrics(xs, "view");
		const cpu = update.sum + view.sum;
		return {
			key,
			n: update.n,
			metrics: {
				updP50: update.p50,
				updP95: update.p95,
				updMax: update.max,
				viewN: view.n,
				viewP50: view.p50,
				viewP95: view.p95,
				viewMax: view.max,
				cpuMs: cpu,
				share: wallMs > 0 ? cpu / wallMs : 0,
			},
		};
	});
}

function windowRow(records) {
	const windows = windowsOf(records);
	const wallMs = sumOf(windows, "window_ms");
	const cpuMs = sumOf(records, "update_sum_ms") + sumOf(records, "view_sum_ms");
	return {
		key: "all",
		n: windows.length,
		metrics: {
			wallS: wallMs / 1000,
			busy: wallMs > 0 ? cpuMs / wallMs : 0,
			overlayOpen: wallMs > 0 ? sumOf(windows, "overlay_open_ms") / wallMs : 0,
			subagentsMax: maxOf(windows, "subagents_running_max"),
			transcriptLinesMax: maxOf(windows, "transcript_lines"),
			droppedWindows: maxOf(windows, "dropped_windows"),
		},
	};
}

const ms = (v) => v.toFixed(2);
const msDelta = (d) => d.toFixed(2);
const pctFmt = (v) => (v * 100).toFixed(1) + "%";
const pctDelta = (d) => (d * 100).toFixed(1) + "pp";
const intFmt = (v) => String(Math.round(v));

const col = (name, field, fmt, fmtDelta) => ({ name, value: (r) => r.metrics[field], fmt, fmtDelta });

export const TUI_TYPE_COLUMNS = [
	col("upd_p50_ms", "updP50", ms, msDelta),
	col("upd_p95_ms", "updP95", ms, msDelta),
	col("upd_max_ms", "updMax", ms, msDelta),
	col("view_n", "viewN", intFmt, intFmt),
	col("view_p50_ms", "viewP50", ms, msDelta),
	col("view_p95_ms", "viewP95", ms, msDelta),
	col("view_max_ms", "viewMax", ms, msDelta),
	col("cpu_ms", "cpuMs", ms, msDelta),
	col("share", "share", pctFmt, pctDelta),
];

export const TUI_WINDOW_COLUMNS = [
	col("wall_s", "wallS", (v) => v.toFixed(1), (d) => d.toFixed(1)),
	col("busy", "busy", pctFmt, pctDelta),
	col("overlay_open", "overlayOpen", pctFmt, pctDelta),
	col("subagents_max", "subagentsMax", intFmt, intFmt),
	col("transcript_lines_max", "transcriptLinesMax", intFmt, intFmt),
	col("dropped_windows", "droppedWindows", intFmt, intFmt),
];

function sections(records, helpers) {
	const wallMs = sumOf(windowsOf(records), "window_ms");
	return {
		by_type: typeRows(records, wallMs, helpers),
		windows: [windowRow(records)],
	};
}

export function runTui({ records, helpers, printTable, asJson }) {
	const s = sections(records, helpers);
	if (asJson) {
		console.log(JSON.stringify(s, null, 1));
		return;
	}
	printTable("tui by message type (n = Update count)", s.by_type, TUI_TYPE_COLUMNS);
	printTable("tui windows (n = windows)", s.windows, TUI_WINDOW_COLUMNS);
}

export function runTuiCompare({ recordsA, recordsB, helpers, emitCompareSections }) {
	const a = sections(recordsA, helpers);
	const b = sections(recordsB, helpers);
	emitCompareSections([
		{ title: "tui by message type", rowsA: a.by_type, rowsB: b.by_type, columns: TUI_TYPE_COLUMNS },
		{ title: "tui windows", rowsA: a.windows, rowsB: b.windows, columns: TUI_WINDOW_COLUMNS },
	]);
}
