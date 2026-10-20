import assert from "node:assert/strict";
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { publicFiles } from "./stage-site.mjs";

// Uses an existing Chrome-compatible debugger; no browser dependency is installed.
const debuggerURL = process.argv[2];
if (!debuggerURL) throw new Error("Usage: node site/scripts/browser-smoke.mjs <CDP URL>");
const root = fileURLToPath(new URL("..", import.meta.url));
const mime = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" };
const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://localhost");
  let file = url.pathname.slice("/MCP-Pi/".length);
  if (!file || file.endsWith("/")) file += "index.html";
  if (!url.pathname.startsWith("/MCP-Pi/") || !publicFiles.includes(file)) {
    res.writeHead(404).end(); return;
  }
  res.setHeader("Content-Type", mime[path.extname(file)] || "text/plain");
  res.end(fs.readFileSync(path.join(root, file)));
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
let ws;
try {
  const tabs = await (await fetch(new URL("/json", debuggerURL))).json();
  ws = new WebSocket(tabs.find(t => t.type === "page").webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });
  let id = 0;
  const pending = new Map();
  const exceptions = [];
  ws.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.method === "Runtime.exceptionThrown") exceptions.push(message.params);
    const handler = pending.get(message.id);
    if (handler) {
      pending.delete(message.id);
      message.error ? handler.reject(message.error) : handler.resolve(message.result);
    }
  };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const requestID = ++id;
    const timer = setTimeout(() => { pending.delete(requestID); reject(new Error(`${method} timed out`)); }, 10000);
    pending.set(requestID, { resolve: value => { clearTimeout(timer); resolve(value); }, reject: error => { clearTimeout(timer); reject(error); } });
    ws.send(JSON.stringify({ id: requestID, method, params }));
  });
  const evaluate = async expression => {
    const result = await call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    assert.equal(result.exceptionDetails, undefined, JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  await call("Runtime.enable");
  await call("Page.enable");
  await call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "light" }] });
  const origin = `http://127.0.0.1:${server.address().port}/MCP-Pi/`;
  let checks = 0;
  for (const width of [320, 390, 768, 1280]) {
    await call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
    await call("Emulation.setTouchEmulationEnabled", { enabled: width < 768 });
    for (const page of ["", "demo/", "guide/"]) {
      await call("Page.navigate", { url: origin + page });
      for (let attempt = 0; attempt < 100; attempt++) {
        if (await evaluate('document.readyState === "complete" && !!document.getElementById("languageToggle")')) break;
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      await evaluate('localStorage.clear(); location.reload()');
      for (let attempt = 0; attempt < 100; attempt++) {
        if (await evaluate('document.readyState === "complete" && !!document.getElementById("languageToggle")')) break;
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      for (const language of ["es", "en"]) {
        await evaluate(`if (document.documentElement.lang !== ${JSON.stringify(language)}) document.getElementById("languageToggle").click()`);
        assert.equal(await evaluate("document.documentElement.lang"), language);
        for (const theme of ["light", "dark", "system"]) {
          for (let i = 0; i < 3; i++) {
            if (await evaluate(`(localStorage.getItem("mcp_pi_theme") || "system") === ${JSON.stringify(theme)}`)) break;
            await evaluate('document.getElementById("themeToggle").click()');
          }
          assert.equal(await evaluate('localStorage.getItem("mcp_pi_theme") || "system"'), theme);
          assert.equal(await evaluate('document.documentElement.classList.contains("dark")'), theme === "dark");
          assert.ok(await evaluate("document.documentElement.scrollWidth <= document.documentElement.clientWidth"), `${width}/${page}/${language}/${theme}: horizontal overflow`);
          assert.ok(await evaluate('document.querySelector("header a").getBoundingClientRect().right <= document.getElementById("languageToggle").getBoundingClientRect().left'), `${width}/${page}/${language}/${theme}: header controls overlap the brand`);
          checks++;
        }
      }
      if (width < 1280) {
        if (width < 768) {
          const point = await evaluate('(() => { const r = document.getElementById("mobileMenuBtn").getBoundingClientRect(); return {x:r.x+r.width/2,y:r.y+r.height/2}; })()');
          await call("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [point] });
          await call("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        } else {
          await evaluate('document.getElementById("mobileMenuBtn").click()');
        }
        assert.equal(await evaluate('document.getElementById("mobileMenuBtn").getAttribute("aria-expanded")'), "true");
        await evaluate('document.getElementById("mobileMenu").dispatchEvent(new KeyboardEvent("keydown", {key:"Escape", bubbles:true}))');
        assert.equal(await evaluate('document.activeElement.id'), "mobileMenuBtn");
      }
      if (page === "demo/") {
        assert.equal(await evaluate('document.getElementById("scenarioSelector").options.length'), 9);
        assert.equal(await evaluate('document.getElementById("scenarioSelector").disabled'), false);
        const records = await evaluate(`Array.from(document.getElementById("scenarioSelector").options, option => {
          const select = document.getElementById("scenarioSelector"); select.value = option.value; select.dispatchEvent(new Event("change"));
          return JSON.parse(document.getElementById("demoAuditJson").textContent);
        })`);
        assert.ok(records.every(record => record.simulated === true && record.persisted === false));
        // A blocked or unavailable clipboard must not crash or report success.
        await evaluate('Object.defineProperty(navigator, "clipboard", {value: undefined, configurable:true}); document.querySelector("[data-copy-target]").click()');
        assert.match(await evaluate('document.querySelector("[data-copy-target]").textContent'), /Could not copy/);
      }
    }
  }
  assert.deepEqual(exceptions, []);
  console.log(`Browser DOM checks: PASS (${checks} page/width/language/theme combinations; nine scenarios; menu; clipboard failure).`);
} finally {
  ws?.close();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
