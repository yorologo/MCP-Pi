import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { stageSite, publicFiles } from "../scripts/stage-site.mjs";

const site = fileURLToPath(new URL("..", import.meta.url));

test("the Pages artifact excludes tooling and arbitrary private files", t => {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "mcp-pages-test-"));
  t.after(() => fs.rmSync(temp, { recursive: true, force: true }));
  const source = path.join(temp, "source");
  fs.cpSync(site, source, { recursive: true });
  fs.writeFileSync(path.join(source, "private.env"), "not-public");
  const output = path.join(temp, "artifact");
  stageSite(source, output);
  const actual = fs.readdirSync(output, { recursive: true }).filter(f => fs.statSync(path.join(output, f)).isFile()).sort();
  assert.deepEqual(actual, [...publicFiles].sort());
  assert.equal(fs.existsSync(path.join(output, "private.env")), false);
  assert.equal(fs.existsSync(path.join(output, "tests")), false);
  assert.throws(() => stageSite(source, output), /must not exist/);

  fs.unlinkSync(path.join(source, "assets/js/site.js"));
  fs.symlinkSync(path.join(source, "private.env"), path.join(source, "assets/js/site.js"));
  const rejected = path.join(temp, "rejected");
  assert.throws(() => stageSite(source, rejected), /Symbolic link/);
  assert.equal(fs.existsSync(rejected), false);
});

test("every local link resolves inside the /MCP-Pi/ published artifact", () => {
  const origin = "https://yorologo.github.io/MCP-Pi/";
  for (const file of publicFiles.filter(f => f.endsWith(".html"))) {
    const html = fs.readFileSync(path.join(site, file), "utf8");
    for (const [, href] of html.matchAll(/(?:href|src)="([^"]+)"/g)) {
      const url = new URL(href, origin + file);
      if (url.origin !== new URL(origin).origin) continue;
      assert.ok(url.pathname.startsWith("/MCP-Pi/"), href);
      let target = url.pathname.slice("/MCP-Pi/".length);
      if (!target || target.endsWith("/")) target += "index.html";
      assert.ok(publicFiles.includes(target), `${file}: ${href} is not published`);
      if (url.hash) {
        assert.ok(fs.readFileSync(path.join(site, target), "utf8").includes(`id="${url.hash.slice(1)}"`), `${file}: ${href} has no anchor`);
      }
    }
  }
});
