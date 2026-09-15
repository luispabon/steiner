#!/usr/bin/env bash
set -euo pipefail

wiki_dir="$(mktemp -d)"
trap 'rm -rf "$wiki_dir"' EXIT

git clone "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.wiki.git" "$wiki_dir"

find "$wiki_dir" -mindepth 1 -maxdepth 1 ! -name ".git" -exec rm -rf {} +

WIKI_DIR="$wiki_dir" python3 - <<'PY'
import os
import pathlib
import re
import shutil

root = pathlib.Path.cwd()
out = pathlib.Path(os.environ["WIKI_DIR"])
docs = root / "docs"

sources = sorted(p for p in docs.rglob("*.md") if "wiki" not in p.relative_to(docs).parts)

def page_name(source):
    relative = source.relative_to(docs).with_suffix("").as_posix()
    if relative == "index":
        return "docs-index"
    if relative.startswith(("user/", "internals/", "maintenance/", "research/")):
        return relative.replace("/", "-")
    return "docs-" + relative.replace("/", "-")

pages = {p: page_name(p) for p in sources}
by_relative = {p.relative_to(root).as_posix(): name for p, name in pages.items()}

link_re = re.compile(r"(?P<prefix>\\]\()(?P<target>[^)#][^)]*)(?P<close>\\))")

def rewrite(text, source):
    def replace(match):
        target = match.group("target")
        if re.match(r"(?:[A-Za-z][A-Za-z0-9+.-]*:|//|/)", target):
            return match.group(0)
        path, sep, anchor = target.partition("#")
        if not path:
            return match.group(0)
        resolved = (source.parent / path).resolve()
        try:
            key = resolved.relative_to(root).as_posix()
        except ValueError:
            return match.group(0)
        name = by_relative.get(key)
        if not name:
            return match.group(0)
        return match.group("prefix") + name + ("#" + anchor if sep else "") + match.group("close")
    return link_re.sub(replace, text)

readme = root / "README.md"
(out / "Home.md").write_text(rewrite(readme.read_text(), readme))
for source, name in pages.items():
    (out / (name + ".md")).write_text(rewrite(source.read_text(), source))
PY

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
