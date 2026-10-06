#!/usr/bin/env node
// Mines steiner session transcripts for the rate of PARALLEL tool calls: how
// often one assistant turn issues more than one tool call. Companion to
// mutate-session-stats.mjs and delegation-session-stats.mjs. Built as the
// before/after metric for #802 (Anthropic wire: all tool results of one
// assistant turn in a single user message).
//
// Usage:
//   node scripts/parallel-tool-stats.mjs [--dir <sessions dir>] [--since YYYY-MM-DD]
//                                        [--model <substr>] [--json]
//
// Defaults to ~/.config/steiner/sessions. Use --since to restrict to sessions
// created on or after a date (the "after" window of a before/after comparison).
// --model keeps only sessions whose model contains <substr> (case-insensitive).
//
// Metric definitions:
//   TURN         one assistant message with a non-empty tool_calls array. Assistant
//                messages without tool calls are not turns and are not counted.
//   CALLS/TURN   tool_calls.length of that message, counting every tool
//                regardless of name.
//   PARALLEL     a turn with 2 or more calls.
//   parallel_rate = turns(>=2 calls) / turns(>=1 call).
//   mean_calls_per_turn = total tool calls / turns(>=1 call).
//   histogram    turns bucketed by calls per turn: 1, 2, 3, 4, 5+.
//   sessions     sessions with at least one turn (sessions_scanned in the total
//                counts every session passing the filters).
//
// Grouping: session files record ONE model string per session (session.model)
// and NO provider, so groups are keyed by model only and there is no --provider
// filter. A session whose model was switched mid-way is attributed entirely to
// the model recorded at save time. Models are not normalised ("gpt-6-luna" and
// "openrouter/free" are separate groups); use --model to pick a family.
//
// Sub-agent (delegated) transcripts are not persisted as sessions; only their
// ledger entries are. Every counted turn is therefore a parent-session turn.
// All lineage generations of a session (including compacted ones) are scanned.

import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

const argv = process.argv.slice(2);
const argOf = (name, fallback) => {
	const i = argv.indexOf(name);
	return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback;
};
const DIR = argOf("--dir", join(homedir(), ".config", "steiner", "sessions"));
const SINCE = argOf("--since", null);
const MODEL = argOf("--model", null)?.toLowerCase() ?? null;
const AS_JSON = argv.includes("--json");

const BUCKETS = ["1", "2", "3", "4", "5+"];
const bucketOf = (n) => (n >= 5 ? "5+" : String(n));
const newGroup = () => ({
	sessions: 0,
	turns_with_tools: 0,
	turns_parallel: 0,
	tool_calls: 0,
	parallel_rate: null,
	mean_calls_per_turn: null,
	histogram: Object.fromEntries(BUCKETS.map((b) => [b, 0])),
});

const sessionFiles = (() => {
	try {
		return readdirSync(DIR);
	} catch (error) {
		if (error.code === "ENOENT") return [];
		throw error;
	}
})();

const total = { sessions_scanned: 0, ...newGroup() };
const perModel = {};

for (const file of sessionFiles) {
	if (!file.endsWith(".json") || file === "index.json") continue;
	let session;
	try {
		session = JSON.parse(readFileSync(join(DIR, file), "utf8"));
	} catch {
		continue;
	}
	if (SINCE && String(session.created_at || "") < SINCE) continue;
	const model = session.model || "unknown";
	if (MODEL && !model.toLowerCase().includes(MODEL)) continue;
	total.sessions_scanned++;
	let sessionTurns = 0;
	const group = (perModel[model] ??= newGroup());

	for (const generation of session.lineage?.generations || []) {
		for (const message of generation.messages || []) {
			if (message.role !== "assistant" || !Array.isArray(message.tool_calls) || message.tool_calls.length === 0) continue;
			const n = message.tool_calls.length;
			sessionTurns++;
			for (const g of [total, group]) {
				g.turns_with_tools++;
				if (n > 1) g.turns_parallel++;
				g.tool_calls += n;
				g.histogram[bucketOf(n)]++;
			}
		}
	}
	if (sessionTurns > 0) {
		total.sessions++;
		group.sessions++;
	}
}

for (const g of [total, ...Object.values(perModel)]) {
	g.parallel_rate = g.turns_with_tools ? g.turns_parallel / g.turns_with_tools : null;
	g.mean_calls_per_turn = g.turns_with_tools ? g.tool_calls / g.turns_with_tools : null;
}

if (AS_JSON) {
	console.log(JSON.stringify({ total, per_model: perModel }, null, 2));
} else {
	const pct = (r) => (r === null ? "n/a" : (100 * r).toFixed(1) + "%");
	const mean = (r) => (r === null ? "n/a" : r.toFixed(2));
	const hist = (g) => BUCKETS.map((b) => `${b}:${g.histogram[b]}`).join(" ");
	const row = (name, g) =>
		`  ${name.padEnd(36)} ${String(g.sessions).padStart(5)} ${String(g.turns_with_tools).padStart(7)} ${String(g.turns_parallel).padStart(7)} ${pct(g.parallel_rate).padStart(7)} ${mean(g.mean_calls_per_turn).padStart(6)}  ${hist(g)}`;
	console.log(`sessions scanned: ${total.sessions_scanned}${SINCE ? `  [since ${SINCE}]` : ""}${MODEL ? `  [model ~ ${MODEL}]` : ""}`);
	console.log(`  ${"model".padEnd(36)} ${"sess".padStart(5)} ${"turns".padStart(7)} ${">=2".padStart(7)} ${"rate".padStart(7)} ${"mean".padStart(6)}  calls-per-turn histogram`);
	const rows = Object.entries(perModel)
		.filter(([, g]) => g.turns_with_tools > 0)
		.sort((a, b) => b[1].turns_with_tools - a[1].turns_with_tools);
	for (const [name, g] of rows) console.log(row(name, g));
	console.log(row("TOTAL", total));
}
