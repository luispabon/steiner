#!/usr/bin/env python3
"""Render repository Markdown and the docs/images tree into a wiki checkout.

Run from the repository root with WIKI_DIR set to the wiki clone and
GITHUB_REPOSITORY set to owner/repo.
"""

import os
import pathlib
import re
import shutil

root = pathlib.Path.cwd()
out = pathlib.Path(os.environ["WIKI_DIR"])
repository = os.environ["GITHUB_REPOSITORY"]
docs = root / "docs"
images = docs / "images"

sources = sorted(p for p in docs.rglob("*.md") if "wiki" not in p.relative_to(docs).parts)

def page_name(source):
    relative = source.relative_to(docs).with_suffix("").as_posix()
    if relative == "index":
        return "docs-index"
    if relative.startswith(("user/", "internals/", "maintenance/")):
        return relative.replace("/", "-")
    return "docs-" + relative.replace("/", "-")

pages = {p: page_name(p) for p in sources}
by_relative = {p.relative_to(root).as_posix(): name for p, name in pages.items()}

link_re = re.compile(r"(?P<prefix>\]\()(?P<target>[^)#][^)]*)(?P<close>\))")

def rewrite(text, source):
    def replace(match):
        target = match.group("target")
        if re.match(r"(?:[A-Za-z][A-Za-z0-9+.-]*:|//|/)", target):
            return match.group(0)
        path, sep, anchor = target.partition("#")
        suffix = "#" + anchor if sep else ""
        if path == "AGENTS.md":
            replacement = "https://github.com/" + repository + "/blob/main/AGENTS.md"
            return match.group("prefix") + replacement + suffix + match.group("close")
        if not path:
            return match.group(0)
        resolved = (source.parent / path).resolve()
        try:
            image = resolved.relative_to(images.resolve()).as_posix()
        except ValueError:
            image = None
        if image is not None:
            return match.group("prefix") + "images/" + image + suffix + match.group("close")
        try:
            key = resolved.relative_to(root).as_posix()
        except ValueError:
            return match.group(0)
        name = by_relative.get(key)
        if not name:
            return match.group(0)
        return match.group("prefix") + name + suffix + match.group("close")
    return link_re.sub(replace, text)

if images.is_dir():
    shutil.copytree(images, out / "images", dirs_exist_ok=True)

readme = root / "README.md"
(out / "Home.md").write_text(rewrite(readme.read_text(), readme))
for source, name in pages.items():
    (out / (name + ".md")).write_text(rewrite(source.read_text(), source))
