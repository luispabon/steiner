#!/bin/sh
#
# tui-bench-compare: A/B benchstat comparison tool for TUI benchmarks.
# Compiles base and head test binaries, runs alternating benchmark loops,
# and outputs a benchstat comparison table.
#
# Usage: tui-bench-compare BASE=<git ref> BENCH=<regex> COUNT=10 [PKG=./internal/tui] [BENCHTIME=1s] [OUT=<dir>]
#
set -eu

# Pin benchstat version (resolved from golang.org/x/perf@latest)
BENCHSTAT_VERSION="v0.0.0-20260929162123-406019bb8b68"

BASE="${BASE:-}"
BENCH="${BENCH:-}"
COUNT="${COUNT:-10}"
PKG="${PKG:-./internal/tui}"
BENCHTIME="${BENCHTIME:-1s}"
OUT="${OUT:-}"

if [ -z "$BASE" ]; then
	echo "error: BASE is required" >&2
	exit 1
fi

if [ -z "$BENCH" ]; then
	echo "error: BENCH is required" >&2
	exit 1
fi

# Create output directory if not specified
if [ -z "$OUT" ]; then
	OUT="$(mktemp -d)"
fi
mkdir -p "$OUT"

# Create temporary worktree for base
TMPDIR_BASE="$(mktemp -d)"
trap 'git worktree remove --force "$TMPDIR_BASE" 2>/dev/null || true; git worktree prune' EXIT

echo "Creating temporary worktree for $BASE..." >&2
git worktree add --detach "$TMPDIR_BASE" "$BASE"

# Compile test binaries
echo "Compiling base test binary..." >&2
(cd "$TMPDIR_BASE" && go test -c -o "$OUT/base.test" "$PKG")

echo "Compiling head test binary..." >&2
go test -c -o "$OUT/head.test" "$PKG"

# Run benchmark loop
i=1
while [ "$i" -le "$COUNT" ]; do
	echo "Running iteration $i/$COUNT..." >&2

	# Run base benchmark
	(
		cd "$TMPDIR_BASE/$PKG"
		"$OUT/base.test" -test.run='^$' -test.bench="$BENCH" -test.benchmem -test.count=1 -test.benchtime="$BENCHTIME" >>"$OUT/base.txt"
	)

	# Run head benchmark
	(
		cd "$PKG"
		"$OUT/head.test" -test.run='^$' -test.bench="$BENCH" -test.benchmem -test.count=1 -test.benchtime="$BENCHTIME" >>"$OUT/head.txt"
	)

	i=$((i + 1))
done

echo "Running benchstat..." >&2
go run "golang.org/x/perf/cmd/benchstat@$BENCHSTAT_VERSION" "$OUT/base.txt" "$OUT/head.txt" | tee "$OUT/benchstat.txt"

echo >&2
echo "Results saved to: $OUT" >&2
