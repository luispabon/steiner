// Flag parsing, model references, the live-call guard and the experiment
// environment for the live compaction bench.

const MAX_PAD_TOKENS = 16000;

// Aliases are YAML keys and output directory names, so keep them to the
// characters both tolerate.
export const ALIAS_RE = /^[A-Za-z0-9._-]+$/;

// Synthesized aliases for provider/id refs share one prefix, so a bench output
// tree names every alias it wrote without needing the original ref.
const SYNTHESIZED_ALIAS_PREFIX = "bench--";

export const USAGE = `usage: node scripts/compaction-bench.mjs --model <a,b> [flags]

flags:
  --model <a,b>                model aliases or provider/id refs (required, comma-separated)
  --anthropic-alias <a,b>      models priced with Anthropic cache weights
  --informational-alias <a,b>  models reported without a pass or fail verdict
  --modes <a,b>                modes to run, subset of off,on (default: off,on)
  --threshold <n>              compaction_threshold_tokens for every model (default: 15000)
  --context-window <n>         context_window for every model; --threshold must be lower
  --seed <n>                   fixture seed; repeat r uses seed + r - 1 (default: the repeat number)
  --files <n>                  notes chain length, 4 to 99 (default: 24)
  --pad-tokens <n>             filler tokens per notes file, 0 to ${MAX_PAD_TOKENS} (default: 3000)
  --max-turns <n>              limits.max_turns for every run (default: 80)
  --repeat <n>                 runs per alias and mode (default: 1)
  --tolerance <n>              allowed percent change for continuity and cost (default: 5)
  --run-timeout-minutes <n>    kill a run that exceeds this many minutes (default: 30)
  --steiner <path>             steiner binary (default: steiner)
  --out <dir>                  write results.md and raw.json here (default: a temp dir)
  --help                       print this usage and exit

A provider/id ref such as opencode-go/beam-free is benched under the
synthesized alias ${SYNTHESIZED_ALIAS_PREFIX}<provider>--<id>, whose private config
pins that provider and id. Output directories and report labels use the
synthesized alias.

The bench makes real provider calls and spends real tokens. It refuses to run
unless STEINER_LIVE_BENCH=1 is set.
`;

// Each flag maps to its args key, its value kind and, for counts, the smallest
// accepted value. --pad-tokens is the only count where zero is meaningful: the
// files keep their CODE and NEXT lines and lose the filler.
const FLAG_SPECS = {
	"--model": { key: "models", kind: "list" },
	"--anthropic-alias": { key: "anthropicAliases", kind: "list" },
	"--informational-alias": { key: "informationalAliases", kind: "list" },
	"--modes": { key: "modes", kind: "list" },
	"--threshold": { key: "threshold", kind: "int", min: 1 },
	"--context-window": { key: "contextWindow", kind: "int", min: 1 },
	"--seed": { key: "seed", kind: "int", min: 0 },
	"--files": { key: "files", kind: "int", min: 1 },
	"--pad-tokens": { key: "padTokens", kind: "int", min: 0 },
	"--max-turns": { key: "maxTurns", kind: "int", min: 1 },
	"--repeat": { key: "repeat", kind: "int", min: 1 },
	"--run-timeout-minutes": { key: "runTimeoutMinutes", kind: "int", min: 1 },
	"--tolerance": { key: "tolerance", kind: "number" },
	"--steiner": { key: "steiner", kind: "string" },
	"--out": { key: "out", kind: "string" },
};

// The three limit variables the bench forwards from its own environment; the
// enable flag is set per mode instead of passed through.
export const SEGMENT_PASSTHROUGH = [
	"STEINER_EXPERIMENT_SEGMENT_MAX_COUNT",
	"STEINER_EXPERIMENT_SEGMENT_MAX_CHAIN_PERCENT",
	"STEINER_EXPERIMENT_SEGMENT_MAX_POST_PERCENT",
];

function usageError(message) {
	return new Error(`${message}\n\n${USAGE}`);
}

function parseIntFlag(flag, value, min) {
	const expected = min === 0 ? "a non-negative integer" : "a positive integer";
	const invalid = () => usageError(`invalid value for ${flag}: ${JSON.stringify(value)} (expected ${expected})`);
	if (!/^\d+$/.test(value)) throw invalid();
	const n = Number(value);
	if (!Number.isSafeInteger(n) || n < min) throw invalid();
	return n;
}

function parseTolerance(flag, value) {
	if (!/^\d+(\.\d+)?$/.test(value)) {
		throw usageError(`invalid value for ${flag}: ${JSON.stringify(value)} (expected a non-negative number)`);
	}
	return Number(value);
}

/**
 * Split one --model ref into its effective alias and, for provider/id refs, the
 * provider and id the generated config must pin. Throws an Error carrying USAGE
 * when the ref cannot become a config alias.
 */
export function resolveModelRef(ref) {
	const slash = ref.indexOf("/");
	if (slash === -1) {
		if (!ALIAS_RE.test(ref)) {
			throw usageError(`invalid --model value: ${JSON.stringify(ref)} (allowed: letters, digits, dot, underscore, dash, or a provider/id ref)`);
		}
		return { ref, alias: ref, provider: null, id: null };
	}
	const provider = ref.slice(0, slash);
	const id = ref.slice(slash + 1);
	const alias = `${SYNTHESIZED_ALIAS_PREFIX}${provider}--${id}`;
	if (!ALIAS_RE.test(provider) || !ALIAS_RE.test(id) || !ALIAS_RE.test(alias)) {
		throw usageError(`invalid --model value: ${JSON.stringify(ref)} (a provider/id ref needs a safe provider and id, and its alias ${JSON.stringify(alias)} must be safe too)`);
	}
	return { ref, alias, provider, id };
}

/** Parse bench flags into run options; throws an Error carrying USAGE on bad input. */
export function parseArgs(argv) {
	const args = {
		help: false,
		models: [],
		anthropicAliases: [],
		informationalAliases: [],
		modes: ["off", "on"],
		threshold: 15000,
		contextWindow: null,
		seed: null,
		files: 24,
		padTokens: 3000,
		maxTurns: 80,
		repeat: 1,
		tolerance: 5,
		runTimeoutMinutes: 30,
		steiner: "steiner",
		out: null,
	};
	const seen = new Set();

	for (let i = 0; i < argv.length; i++) {
		const raw = argv[i];
		let name = raw;
		let value;
		const eq = raw.startsWith("--") ? raw.indexOf("=") : -1;
		if (eq > 2) {
			name = raw.slice(0, eq);
			value = raw.slice(eq + 1);
		}
		if (name === "--help") {
			if (value !== undefined) throw usageError("--help takes no value");
			args.help = true;
			continue;
		}
		const spec = FLAG_SPECS[name];
		if (!spec) {
			throw usageError(raw.startsWith("-") ? `unknown flag: ${name}` : `unexpected argument: ${raw}`);
		}
		if (value === undefined) {
			const next = argv[i + 1];
			if (next === undefined || (next.length > 2 && next.startsWith("--"))) {
				throw usageError(`missing value for ${name}`);
			}
			value = next;
			i++;
		}
		if (spec.kind === "list") {
			const values = value
				.split(",")
				.map((v) => v.trim())
				.filter((v) => v !== "");
			if (values.length === 0) throw usageError(`${name} needs at least one value`);
			if (!seen.has(spec.key)) {
				args[spec.key] = [];
				seen.add(spec.key);
			}
			args[spec.key].push(...values);
			continue;
		}
		if (spec.kind === "int") {
			args[spec.key] = parseIntFlag(name, value, spec.min);
			continue;
		}
		if (spec.kind === "number") {
			args[spec.key] = parseTolerance(name, value);
			continue;
		}
		args[spec.key] = value;
	}

	if (args.help) return args;
	if (args.models.length === 0) {
		throw usageError("--model is required (comma-separated model aliases)");
	}
	args.modelEntries = args.models.map(resolveModelRef);
	const aliasOwners = new Map();
	for (const entry of args.modelEntries) {
		const owner = aliasOwners.get(entry.alias);
		if (owner !== undefined && owner !== entry.ref) {
			throw usageError(`--model values ${JSON.stringify(owner)} and ${JSON.stringify(entry.ref)} both map to alias ${JSON.stringify(entry.alias)}`);
		}
		aliasOwners.set(entry.alias, entry.ref);
	}
	if (args.contextWindow !== null && args.threshold >= args.contextWindow) {
		throw usageError(
			`--threshold ${args.threshold} must be less than --context-window ${args.contextWindow}: steiner rejects a compaction threshold at or above the context window`,
		);
	}
	args.modes = [...new Set(args.modes)];
	for (const mode of args.modes) {
		if (mode !== "off" && mode !== "on") {
			throw usageError(`invalid --modes value: ${JSON.stringify(mode)} (allowed: off,on)`);
		}
	}
	for (const [flag, aliases] of [
		["--anthropic-alias", args.anthropicAliases],
		["--informational-alias", args.informationalAliases],
	]) {
		for (const alias of aliases) {
			if (!args.models.includes(alias)) {
				throw usageError(`${flag} value ${JSON.stringify(alias)} is not in --model`);
			}
		}
	}
	if (args.padTokens > MAX_PAD_TOKENS) {
		throw usageError(
			`--pad-tokens ${args.padTokens} exceeds ${MAX_PAD_TOKENS}: the read tool caps a single output at 65536 runes, so more padding is silently truncated`,
		);
	}
	return args;
}

/** Return null when the live-bench opt-in is set, else the refusal message. */
export function checkGuard(env) {
	if (env && env.STEINER_LIVE_BENCH === "1") return null;
	return "refusing to run: the compaction bench makes real provider calls and spends real tokens; set STEINER_LIVE_BENCH=1 to confirm";
}

/** Build the child environment: the experiment flag plus only the three segment limit passthroughs. */
export function experimentEnv(env, mode) {
	const flag = mode === "on" ? "1" : mode === "off" ? "0" : null;
	if (flag === null) throw new Error(`unknown mode: ${JSON.stringify(mode)} (allowed: off,on)`);
	const out = { STEINER_EXPERIMENT_SEGMENTED_COMPACTION: flag };
	for (const key of SEGMENT_PASSTHROUGH) {
		if (env && env[key] !== undefined) out[key] = env[key];
	}
	return out;
}
