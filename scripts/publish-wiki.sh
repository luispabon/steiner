#!/usr/bin/env bash
set -euo pipefail

wiki_dir="$(mktemp -d)"
trap 'rm -rf "$wiki_dir"' EXIT

git clone "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.wiki.git" "$wiki_dir"

find "$wiki_dir" -mindepth 1 -maxdepth 1 ! -name ".git" -exec rm -rf {} +

WIKI_DIR="$wiki_dir" python3 scripts/wiki-render.py

cp docs/wiki/_Sidebar.md "$wiki_dir/_Sidebar.md"
if [ -f docs/wiki/_Footer.md ]; then cp docs/wiki/_Footer.md "$wiki_dir/_Footer.md"; fi

cd "$wiki_dir"
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git add -A
if git diff --cached --quiet; then
  echo "No wiki changes to publish"
  exit 0
fi
git commit -m "Publish wiki from ${GITHUB_SHA::7}"
git push
