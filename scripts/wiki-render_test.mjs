import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";

const scripts = dirname(fileURLToPath(import.meta.url));
const script = join(scripts, "wiki-render.py");

const readme = [
	"# steiner",
	"",
	"![Steiner screenshot](docs/images/screenshot.png)",
	"",
	"See [interactive mode](docs/user/interactive.md).",
	"External [site](https://example.com/docs) stays as is.",
	"",
].join("\n");

const interactive = [
	"# Interactive mode",
	"",
	"![Steiner screenshot](../images/tui.png)",
	"",
	"![Flow](../images/diagrams/flow.png#layout)",
	"",
].join("\n");

function render({ repository = "owner/steiner", readmeText = readme } = {}) {
	const repo = mkdtempSync(join(tmpdir(), "steiner-wiki-render-"));
	const wiki = join(repo, "wiki");
	mkdirSync(wiki);
	writeFileSync(join(repo, "README.md"), readmeText);
	mkdirSync(join(repo, "docs", "user"), { recursive: true });
	writeFileSync(join(repo, "docs", "user", "interactive.md"), interactive);
	mkdirSync(join(repo, "docs", "images", "diagrams"), { recursive: true });
	writeFileSync(join(repo, "docs", "images", "screenshot.png"), "screenshot-bytes");
	writeFileSync(join(repo, "docs", "images", "tui.png"), "tui-bytes");
	writeFileSync(join(repo, "docs", "images", "diagrams", "flow.png"), "flow-bytes");
	const env = { ...process.env, WIKI_DIR: wiki };
	if (repository === null) {
		delete env.GITHUB_REPOSITORY;
	} else {
		env.GITHUB_REPOSITORY = repository;
	}
	execFileSync("python3", [script], { cwd: repo, env });
	return { repo, wiki };
}

function imageLinks(wiki) {
	const links = [];
	for (const name of readdirSync(wiki)) {
		if (!name.endsWith(".md")) continue;
		for (const match of readFileSync(join(wiki, name), "utf8").matchAll(/\]\((images\/[^)#]+)/g)) {
			links.push(match[1]);
		}
	}
	return links.sort();
}

test("rewrites docs/images links to wiki-root-relative paths", () => {
	const { repo, wiki } = render();
	try {
		const home = readFileSync(join(wiki, "Home.md"), "utf8");
		assert.match(home, /!\[Steiner screenshot\]\(images\/screenshot\.png\)/);
		assert.match(home, /\[interactive mode\]\(user-interactive\)/);
		assert.match(home, /\[site\]\(https:\/\/example\.com\/docs\)/);
		const page = readFileSync(join(wiki, "user-interactive.md"), "utf8");
		assert.match(page, /!\[Steiner screenshot\]\(images\/tui\.png\)/);
		assert.match(page, /!\[Flow\]\(images\/diagrams\/flow\.png#layout\)/);
		assert.ok(!home.includes("docs/images"), "Home.md keeps a local image path");
		assert.ok(!page.includes("../images"), "page keeps a local image path");
	} finally {
		rmSync(repo, { recursive: true, force: true });
	}
});

test("copies the whole docs/images tree and links land on copied files", () => {
	const { repo, wiki } = render();
	try {
		assert.equal(readFileSync(join(wiki, "images", "screenshot.png"), "utf8"), "screenshot-bytes");
		assert.equal(readFileSync(join(wiki, "images", "tui.png"), "utf8"), "tui-bytes");
		assert.equal(readFileSync(join(wiki, "images", "diagrams", "flow.png"), "utf8"), "flow-bytes");
		assert.deepEqual(imageLinks(wiki), ["images/diagrams/flow.png", "images/screenshot.png", "images/tui.png"]);
		for (const link of imageLinks(wiki)) {
			assert.ok(existsSync(join(wiki, link)), `missing copied image for ${link}`);
		}
	} finally {
		rmSync(repo, { recursive: true, force: true });
	}
});

test("reads GITHUB_REPOSITORY only when rewriting an AGENTS.md link", () => {
	const withoutEnv = render({ repository: null });
	try {
		assert.match(readFileSync(join(withoutEnv.wiki, "Home.md"), "utf8"), /!\[Steiner screenshot\]\(images\/screenshot\.png\)/);
	} finally {
		rmSync(withoutEnv.repo, { recursive: true, force: true });
	}
	const agents = "# steiner\n\nSee [contributing](AGENTS.md#work-loop).\n";
	const withEnv = render({ readmeText: agents });
	try {
		assert.match(
			readFileSync(join(withEnv.wiki, "Home.md"), "utf8"),
			/\[contributing\]\(https:\/\/github\.com\/owner\/steiner\/blob\/main\/AGENTS\.md#work-loop\)/,
		);
	} finally {
		rmSync(withEnv.repo, { recursive: true, force: true });
	}
});
