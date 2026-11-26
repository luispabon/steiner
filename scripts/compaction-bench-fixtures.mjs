// Notes-chain fixtures and the YAML configs a bench run or probe writes.

import { ALIAS_RE, isModelId } from "./compaction-bench-args.mjs";

const FILLER_WORDS = [
	"haystack", "meadow", "lantern", "harbour", "tram", "copper", "saffron", "brisk",
	"delta", "wobble", "pantry", "hollow", "gantry", "moss", "plume", "quartz",
];

const CODE_LETTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
const CODE_DIGITS = "0123456789";

function mulberry32(seed) {
	let state = seed >>> 0;
	return function next() {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

function pick(rand, alphabet) {
	return alphabet[Math.floor(rand() * alphabet.length)];
}

function nextCode(rand, used) {
	for (;;) {
		const code = `${pick(rand, CODE_LETTERS)}${pick(rand, CODE_DIGITS)}-${pick(rand, CODE_LETTERS)}${pick(rand, CODE_LETTERS)}${pick(rand, CODE_DIGITS)}${pick(rand, CODE_DIGITS)}`;
		if (!used.has(code)) {
			used.add(code);
			return code;
		}
	}
}

// Filler lines stay just under the read tool's 2000-rune per-line clip
// (internal/tool/builtin/render_bounds.go), so a file's padding arrives in a
// few lines and a default 200-line read returns it in one page.
const FILLER_LINE_TARGET_CHARS = 1900;

function fillerLines(rand, targetChars) {
	const lines = [];
	let total = 0;
	while (total < targetChars) {
		const limit = Math.min(FILLER_LINE_TARGET_CHARS, targetChars - total);
		const words = [];
		let length = 0;
		while (length < limit) {
			const word = FILLER_WORDS[Math.floor(rand() * FILLER_WORDS.length)];
			const extra = words.length === 0 ? word.length : word.length + 1;
			if (words.length > 0 && length + extra > limit) break;
			words.push(word);
			length += extra;
		}
		const line = words.join(" ");
		lines.push(line);
		total += line.length + 1;
	}
	return lines;
}

/** Generate the deterministic notes chain, its codes and the seeded correction. */
export function generateFixtures(seed, files, padTokens) {
	if (!Number.isInteger(files) || files < 4 || files > 99) {
		throw new Error(`files must be an integer from 4 to 99, got ${JSON.stringify(files)}`);
	}
	if (!Number.isInteger(padTokens) || padTokens < 0) {
		throw new Error(`padTokens must be a non-negative integer, got ${JSON.stringify(padTokens)}`);
	}
	const rand = mulberry32(seed);
	const names = Array.from({ length: files }, (_, i) => `f${String(i + 1).padStart(2, "0")}`);
	const used = new Set();
	const codes = {};
	for (const name of names) codes[name] = nextCode(rand, used);

	// The correction must land strictly after its target f03 and in the second
	// half, so a small chain (files=4) still carries it in f04 and never f01-f03.
	const firstCandidate = Math.max(3, Math.floor(files / 2));
	const inFile = names[firstCandidate + Math.floor(rand() * (files - firstCandidate))];
	const newCode = nextCode(rand, used);
	const correction = { inFile, target: "f03", oldCode: codes.f03, newCode };

	const outFiles = names.map((name, index) => {
		const next = index + 1 < names.length ? `${names[index + 1]}.txt` : "END";
		const lines = [`CODE: ${codes[name]}`, `NEXT: ${next}`];
		if (name === inFile) {
			lines.push(`CORRECTION: the code in ${correction.target} is now ${newCode}`);
		}
		lines.push(...fillerLines(rand, padTokens * 4));
		return { name: `notes/${name}.txt`, content: `${lines.join("\n")}\n` };
	});

	return { files: outFiles, codes, correction };
}

/** Render the task prompt that walks the generated notes chain one file at a time. */
export function taskPrompt(files, runId = null) {
	const lines = [];
	// A per-run first line keeps every run's first request byte-distinct, so no
	// run starts from a cache entry warmed by another run.
	if (runId !== null) lines.push(`Run id: ${runId}`);
	lines.push(
		`Read the notes in notes/ one file at a time. The chain has ${files.length} files.`,
		"Start at notes/f01.txt. Each file starts with a CODE line and a NEXT line naming the next file.",
		"Open only the file named by NEXT. Never read a file twice. Never list or search the notes directory.",
		"Use only the read tool on each file. Do not use bash, grep, cat, sed or any other command to inspect files.",
		"Apply any CORRECTION line you find to the code it names.",
		'When you reach NEXT: END, reply with one line per file in the form "fNN: CODE", using the corrected code where a correction applies.',
		"Reply with those lines only.",
	);
	return lines.join("\n");
}

const ALIAS_HINT = "allowed: letters, digits, dot, underscore, dash";

function diagnosticsLines(diagnosticsDir) {
	if (typeof diagnosticsDir !== "string" || diagnosticsDir === "") {
		throw new Error("diagnosticsDir must be a non-empty string");
	}
	return [
		"diagnostics:",
		"  enabled: true",
		`  dir: ${JSON.stringify(diagnosticsDir)}`,
		"  streams:",
		"    cache: true",
		"    provider: true",
	];
}

function modelDefinitionLines({ alias, provider, id }) {
	if (provider === null && id === null) return [];
	if (typeof provider !== "string" || !ALIAS_RE.test(provider) || !isModelId(id)) {
		throw new Error(`invalid provider/id for alias ${JSON.stringify(alias)}: ${JSON.stringify(provider)}/${JSON.stringify(id)}`);
	}
	if (typeof alias !== "string" || !ALIAS_RE.test(alias)) {
		throw new Error(`invalid model alias: ${JSON.stringify(alias)} (${ALIAS_HINT})`);
	}
	// The id is quoted because it may hold YAML syntax such as name:tag.
	return ["models:", "  definitions:", `    ${alias}:`, `      provider: ${provider}`, `      id: ${JSON.stringify(id)}`];
}

/** Render the per-alias YAML config with the limits block and the cache and provider diagnostics streams. */
export function buildBenchConfig({ alias, provider = null, id = null, threshold, contextWindow = null, diagnosticsDir }) {
	if (typeof alias !== "string" || !ALIAS_RE.test(alias)) {
		throw new Error(`invalid model alias: ${JSON.stringify(alias)} (${ALIAS_HINT})`);
	}
	if (!Number.isInteger(threshold) || threshold <= 0) {
		throw new Error(`threshold must be a positive integer, got ${JSON.stringify(threshold)}`);
	}
	if (contextWindow !== null && (!Number.isInteger(contextWindow) || contextWindow <= 0)) {
		throw new Error(`contextWindow must be a positive integer or null, got ${JSON.stringify(contextWindow)}`);
	}
	const limits = [`          compaction_threshold_tokens: ${threshold}`];
	if (contextWindow !== null) limits.push(`          context_window: ${contextWindow}`);
	return [
		...modelDefinitionLines({ alias, provider, id }),
		...(provider === null ? ["models:", "  definitions:", `    ${alias}:`] : []),
		"      advanced:",
		"        limits:",
		...limits,
		...diagnosticsLines(diagnosticsDir),
		"",
	].join("\n");
}

/** Render the probe's YAML config: diagnostics only, so no threshold can exceed the alias context window. */
export function buildProbeConfig({ diagnosticsDir, alias = null, provider = null, id = null }) {
	const definitions = provider === null ? [] : modelDefinitionLines({ alias, provider, id });
	return [...definitions, ...diagnosticsLines(diagnosticsDir), ""].join("\n");
}
