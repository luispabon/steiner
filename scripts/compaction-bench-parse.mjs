// Parse a run's artefacts (session log JSONL, compaction log text, exec stdout)
// into decisions, events and a recall score.

const SEGMENT_DECISION_RE = /^segment_decision action=(append|rebase|plain) reason=([a-z][a-z-]*) sequence=(\d+) chain_tokens=(\d+) projected_post_tokens=(\d+) budget=(\d+)$/;

/** Parse whole-line segment_decision records from a compaction log, ignoring embedded text. */
export function parseSegmentDecisions(text) {
	const decisions = [];
	for (const raw of String(text).split(/\r?\n/)) {
		const match = SEGMENT_DECISION_RE.exec(raw.trim());
		if (!match) continue;
		decisions.push({
			action: match[1],
			reason: match[2],
			sequence: Number(match[3]),
			chainTokens: Number(match[4]),
			projectedPostTokens: Number(match[5]),
			budget: Number(match[6]),
		});
	}
	return decisions;
}

/** Count decision kinds; plain counts as a rebase fallback and separately as a plain fallback. */
export function summarizeDecisions(decisions) {
	let appends = 0;
	let rebases = 0;
	let plainFallbacks = 0;
	let chainStarts = 0;
	let maxSegments = 0;
	for (const decision of decisions) {
		if (decision.action === "append") {
			appends++;
			maxSegments = Math.max(maxSegments, decision.sequence);
		} else if (decision.action === "rebase") {
			if (decision.reason !== "chain-start") rebases++;
			maxSegments = Math.max(maxSegments, decision.sequence);
		} else {
			plainFallbacks++;
			rebases++;
		}
		if (decision.reason === "chain-start") chainStarts++;
	}
	return { decisions: decisions.length, appends, rebases, plainFallbacks, chainStarts, maxSegments };
}

const READ_NOTES_RE = /(?:^|\/)notes\/(f\d+)\.txt$/;
const READ_BASENAME_RE = /(?:^|\/)(f\d+)\.txt$/;
// A fixture path starts its value or follows a shell-ish boundary, so a value
// that merely contains "notes" (release-notes/, footnotes/, docs/notes/) does
// not count. A fixture file name is inside that same directory.
const FIXTURE_PATH_RE = /(?:^|[\s"'=:(])(?:\.\/)?notes(?:\/|$|[\s"')])/;
// An absolute path to a fixture file counts wherever it sits, since the segment
// before the file name is exactly notes.
const FIXTURE_FILE_RE = /(?:^|\/)notes\/f\d+\.txt(?![\w.])/;
const COMPACTION_START_SEVERITIES = new Set(["compacting", "skipped"]);

function readFileName(path) {
	const match = READ_NOTES_RE.exec(path) ?? READ_BASENAME_RE.exec(path);
	return match ? match[1] : null;
}

function stringValues(value, out = []) {
	if (typeof value === "string") out.push(value);
	else if (Array.isArray(value)) for (const item of value) stringValues(item, out);
	else if (value !== null && typeof value === "object") for (const item of Object.values(value)) stringValues(item, out);
	return out;
}

function escapeRegExp(text) {
	return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// The run's own notes directory, for an absolute path that ends at the directory
// instead of naming a file. No notes dir means the parser cannot judge those.
function notesDirPattern(notesDir) {
	if (typeof notesDir !== "string") return null;
	const dir = notesDir.replace(/\/+$/, "");
	if (dir === "") return null;
	return new RegExp(`(?:^|[\\s"'])${escapeRegExp(dir)}\\/?(?:$|[\\s"')])`);
}

function referencesFixture(argumentValue, notesDirRe = null) {
	return stringValues(argumentValue).some(
		(text) => FIXTURE_PATH_RE.test(text) || FIXTURE_FILE_RE.test(text) || notesDirRe?.test(text) === true,
	);
}

function scopedToSubAgent(record) {
	const scope = record.scope;
	return Boolean(scope && typeof scope === "object" && scope.agent_type);
}

// Both call kinds report cache the same way: a counter present marks the call
// as reporting, and an absent counter reads as zero.
function cacheFields(payload) {
	const hasRead = payload.cache_read_tokens !== undefined;
	const hasCreate = payload.cache_create_tokens !== undefined;
	const cacheReported = payload.cache_reported_input_tokens !== undefined || hasRead || hasCreate;
	return {
		cacheReported,
		cacheReadTokens: payload.cache_read_tokens ?? 0,
		cacheCreateTokens: payload.cache_create_tokens ?? 0,
	};
}

function parseModelCall(payload) {
	return {
		turn: payload.turn ?? 0,
		promptTokens: payload.prompt_tokens ?? 0,
		durationMs: payload.duration_ms ?? 0,
		ttftMs: payload.ttft_ms ?? 0,
		...cacheFields(payload),
	};
}

function parseCompactionCall(payload) {
	// The applied compaction payload's prompt_tokens is the post-compaction prompt
	// estimate, not the summarizer's input (internal/agent/events.go sets it from
	// afterFit). The summary call total is uncached input plus both cache counters.
	const promptTokens = (payload.input_tokens ?? 0) + (payload.cache_read_tokens ?? 0) + (payload.cache_create_tokens ?? 0);
	return {
		promptTokens,
		// Applied compaction events report no call duration in the session log; the
		// provider diagnostics stream carries it instead. A payload that gains one
		// is still summed.
		durationMs: payload.duration_ms ?? 0,
		ttftMs: payload.ttft_ms ?? 0,
		...cacheFields(payload),
	};
}

// The retained tail is the retained_tokens of the last stage that applied. A
// failed stage is appended after the applied ones, so it never supplies the tail.
function finalAppliedStage(stages) {
	if (!Array.isArray(stages)) return null;
	for (let i = stages.length - 1; i >= 0; i--) {
		const stage = stages[i];
		if (stage === null || typeof stage !== "object" || stage.failed === true) continue;
		return stage;
	}
	return null;
}

function stageNames(stages) {
	if (!Array.isArray(stages)) return [];
	return stages
		.filter((stage) => stage !== null && typeof stage === "object")
		.map((stage) => stage.stage)
		.filter((name) => typeof name === "string");
}

/** Parse a session log JSONL into ordered reads, applied compactions and model-call cache usage. */
export function parseRunLog(text, { notesDir = null } = {}) {
	const events = [];
	const modelCalls = [];
	const compactionCalls = [];
	const retainedTail = [];
	let compactions = 0;
	let emergencyCompactions = 0;
	let malformed = 0;
	let nonReadFileAccess = 0;
	let contextWindow = null;
	let thresholdTokens = null;
	const notesDirRe = notesDirPattern(notesDir);

	for (const raw of String(text).split(/\r?\n/)) {
		const line = raw.trim();
		if (line === "") continue;
		let record;
		try {
			record = JSON.parse(line);
		} catch {
			malformed++;
			continue;
		}
		if (record === null || typeof record !== "object" || Array.isArray(record)) {
			malformed++;
			continue;
		}
		const payload = record.payload && typeof record.payload === "object" ? record.payload : {};
		if (record.type === "tool_call_started") {
			if (scopedToSubAgent(record)) {
				// A sub-agent cannot carry the main conversation's context, so fixture
				// access there scores nothing, not even through the read tool.
				if (referencesFixture(payload.arguments, notesDirRe)) nonReadFileAccess++;
				continue;
			}
			if (payload.tool !== "read") {
				// The task allows only the read tool; any other tool that reaches the
				// fixtures wins the score outside the scored access path.
				if (referencesFixture(payload.arguments, notesDirRe)) nonReadFileAccess++;
				continue;
			}
			const path = payload.arguments && payload.arguments.path;
			if (typeof path !== "string") continue;
			const file = readFileName(path);
			if (!file) continue;
			events.push({ kind: "read", file, turn: payload.turn ?? 0 });
			continue;
		}
		if (record.type === "model_call_finished") {
			// Failed calls carry no usable usage and sub-agent calls are not part of the
			// main conversation's cache economics; both stay out, as they do for reads
			// and compactions.
			if (payload.success !== true) continue;
			if (scopedToSubAgent(record)) continue;
			modelCalls.push(parseModelCall(payload));
			continue;
		}
		if (record.type === "context_diagnostics") {
			if (payload.kind !== "compaction") continue;
			if (COMPACTION_START_SEVERITIES.has(payload.severity)) continue;
			if (scopedToSubAgent(record)) continue;
			compactions++;
			events.push({ kind: "compaction", index: compactions });
			compactionCalls.push(parseCompactionCall(payload));
			if (stageNames(payload.stages).includes("emergency")) emergencyCompactions++;
			const stage = finalAppliedStage(payload.stages);
			if (stage !== null && Number.isFinite(stage.retained_tokens)) retainedTail.push(stage.retained_tokens);
			if (Number.isFinite(payload.context_window) && payload.context_window > 0) contextWindow = payload.context_window;
			if (Number.isFinite(payload.compaction_threshold_tokens) && payload.compaction_threshold_tokens > 0) {
				thresholdTokens = payload.compaction_threshold_tokens;
			}
		}
	}

	return {
		events,
		modelCalls,
		compactionCalls,
		compactions,
		emergencyCompactions,
		retainedTail,
		contextWindow,
		thresholdTokens,
		malformed,
		nonReadFileAccess,
	};
}

/** Return only the ordered read events from a session log. */
export const parseToolReads = (text) => parseRunLog(text).events.filter((event) => event.kind === "read");

/**
 * Parse the provider diagnostics stream into per-call retry and outcome counts.
 * Records come either wrapped in the diagnostics envelope (kind provider, with
 * the call record under payload) or as the bare provider stream record.
 */
export function parseProviderLog(text) {
	const out = { calls: 0, retries: 0, rateLimited: 0, errors: 0 };
	for (const raw of String(text).split(/\r?\n/)) {
		const line = raw.trim();
		if (line === "") continue;
		let record;
		try {
			record = JSON.parse(line);
		} catch {
			continue;
		}
		if (record === null || typeof record !== "object" || Array.isArray(record)) continue;
		const payload = record.kind === "provider" && record.payload && typeof record.payload === "object" ? record.payload : record;
		if (typeof payload.attempts !== "number" && typeof payload.outcome !== "string") continue;
		out.calls++;
		if (Number.isFinite(payload.attempts)) out.retries += Math.max(0, payload.attempts - 1);
		if (payload.error_class === "rate_limited") out.rateLimited++;
		if (payload.outcome === "exhausted" || payload.outcome === "cancelled") out.errors++;
	}
	return out;
}

const EVENT_LABEL_RE = /^[a-z_]+: /;
const MAX_TURNS_RE = /^status: stopped after \d+ turns?: reached the max turn limit/;
const ASSISTANT_ANSWER_RE = /^turn=\d+ role=\S+ content=([\s\S]*)$/;

/** Parse exec stdout into the last assistant answer and whether the max-turn limit stopped the run. */
export function parseStdout(text) {
	const lines = String(text).split(/\r?\n/);
	let maxTurnsHit = false;
	for (const line of lines) {
		if (MAX_TURNS_RE.test(line)) maxTurnsHit = true;
	}

	let finalAnswer = null;
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
		if (!line.startsWith("assistant: ")) continue;
		const match = ASSISTANT_ANSWER_RE.exec(line.slice("assistant: ".length));
		if (!match) continue;
		const parts = [match[1]];
		let j = i + 1;
		for (; j < lines.length; j++) {
			const next = lines[j];
			if (EVENT_LABEL_RE.test(next) || next.startsWith("assistant> ")) break;
			parts.push(next);
		}
		finalAnswer = parts.join("\n").trimEnd();
		i = j - 1;
	}

	return { finalAnswer, maxTurnsHit };
}

function lossPoint(events, file) {
	const firstRead = events.findIndex((event) => event.kind === "read" && event.file === file);
	if (firstRead === -1) return "unattributed";
	const compaction = events.slice(firstRead + 1).find((event) => event.kind === "compaction");
	return compaction ? compaction.index : "unattributed";
}

/** Score the final answer against the fixtures, attributing each miss and flagging re-reads. */
export function scoreRecall({ answer, fixtures, events }) {
	const answerLines = new Map();
	const text = typeof answer === "string" ? answer : "";
	const lineRe = /^\s*(f\d{2})\s*:\s*(\S+)\s*$/gim;
	let match;
	while ((match = lineRe.exec(text)) !== null) {
		answerLines.set(match[1].toLowerCase(), match[2]);
	}

	const names = fixtures.files.map((file) => file.name.replace(/^notes\//, "").replace(/\.txt$/, ""));
	const perFile = [];
	let exact = 0;
	for (const name of names) {
		const isTarget = name === fixtures.correction.target;
		const expected = isTarget ? fixtures.correction.newCode : fixtures.codes[name];
		const got = answerLines.get(name.toLowerCase());
		let ok = got !== undefined && got === expected;
		if (isTarget) ok = ok && !text.includes(fixtures.correction.oldCode);
		if (ok) {
			exact++;
			perFile.push({ file: name, status: "exact", lostAt: null });
		} else {
			perFile.push({ file: name, status: "lost", lostAt: lossPoint(events, name) });
		}
	}

	const invalidReasons = [];
	for (const name of names) {
		const reads = [];
		events.forEach((event, position) => {
			if (event.kind === "read" && event.file === name) reads.push(position);
		});
		if (reads.length < 2) continue;
		// Any later read of the file counts, not just the second one: a compaction
		// between the first read and a later one still drops the file from context.
		const firstCompaction = events.findIndex((event, position) => position > reads[0] && event.kind === "compaction");
		if (firstCompaction === -1) continue;
		if (!reads.some((position) => position > firstCompaction)) continue;
		invalidReasons.push(`${name} read again after compaction ${events[firstCompaction].index}`);
	}

	return {
		exact,
		lost: perFile.length - exact,
		total: perFile.length,
		invalid: invalidReasons.length > 0,
		invalidReason: invalidReasons.length > 0 ? invalidReasons.join("; ") : null,
		perFile,
	};
}
