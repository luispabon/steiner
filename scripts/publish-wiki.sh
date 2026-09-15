#!/usr/bin/env bash
set -euo pipefail

wiki_dir="$(mktemp -d)"
trap 'rm -rf "$wiki_dir"' EXIT

git clone "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.wiki.git" "$wiki_dir"

find "$wiki_dir" -mindepth 1 -maxdepth 1 ! -name ".git" -exec rm -rf {} +

# Flatten repository docs into stable, collision-free Wiki page names.
sed -E \
  -e 's#\]\(docs/([^)/]+/)?([^)/]+)\.md([^)]*)\)#](\1\2\3)#g' \
  -e 's#docs/([^)/]+/)?([^)/]+)\.md#\1\2#g' \
  README.md > "$wiki_dir/Home.md"

while IFS= read -r -d '' source; do
  relative="${source#docs/}"
  page="${relative%.md}"
  page="${page//\//-}"
  case "$page" in
    wiki-*) continue ;;
    index) page="docs-index" ;;
    user-*) ;;
    internals-*) ;;
    maintenance-*) ;;
    research-*) ;;
    *) page="docs-$page" ;;
  esac
  # Rewrite repository-relative links to the flattened page names.
  sed -E \
    -e 's#\]\((\.\./)?(user|internals|maintenance|research)/([^)#]+)\.md([^)]*)\)#](\2-\3\4)#g' \
    -e 's#\]\(([^)#/]+)\.md([^)]*)\)#](\1\2)#g' \
    "$source" > "$wiki_dir/$page.md"
done < <(find docs -type f -name '*.md' ! -path 'docs/wiki/*' -print0 | sort -z)

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
