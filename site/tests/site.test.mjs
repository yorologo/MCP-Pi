import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const pages = ["index.html", "demo/index.html", "guide/index.html"].map(file => ({file, html: fs.readFileSync(path.join(siteDir, file), "utf8")}));
const html = pages.map(page => page.html).join("\n");
const siteJs = fs.readFileSync(path.join(siteDir, "assets", "js", "site.js"), "utf8");
const demoJs = fs.readFileSync(path.join(siteDir, "assets", "js", "demo-scenarios.js"), "utf8");

// Load DEMO_SCENARIOS in node VM
const demoContext = { module: { exports: {} }, window: {} };
vm.runInNewContext(demoJs, demoContext);
const DEMO_SCENARIOS = demoContext.module.exports.DEMO_SCENARIOS || demoContext.window.DEMO_SCENARIOS;

const textKeys = [...html.matchAll(/data-i18n="([^"]+)"/g)].map((m) => m[1]);
const ariaKeys = [...html.matchAll(/data-i18n-aria="([^"]+)"/g)].map((m) => m[1]);
const altKeys = [...html.matchAll(/data-i18n-alt="([^"]+)"/g)].map((m) => m[1]);

function createElement(tagName = "div", attributes = {}) {
  const children = [];
  let innerHtmlVal = "";
  let textContentVal = "__untranslated__";

  return {
    tagName: tagName.toUpperCase(),
    attributes: { ...attributes },
    className: attributes.class || "",
    value: attributes.value || "",
    options: [],
    handlers: {},
    children,
    get innerHTML() { return innerHtmlVal; },
    set innerHTML(val) {
      innerHtmlVal = val;
      textContentVal = String(val).replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
    },
    get textContent() { return textContentVal; },
    set textContent(val) {
      textContentVal = val;
      innerHtmlVal = val;
    },
    getAttribute(name) { return this.attributes[name] ?? null; },
    setAttribute(name, value) {
      this.attributes[name] = value;
      if (name === "class") this.className = value;
      if (name === "value") this.value = value;
    },
    addEventListener(name, handler) { this.handlers[name] = handler; },
    click() { this.handlers.click?.(); },
    dispatchEvent(event) { this.handlers[event.type]?.(event); },
    appendChild(child) {
      children.push(child);
      if (this.tagName === "SELECT" && child.tagName === "OPTION") {
        this.options.push(child);
      }
    },
    querySelectorAll(selector) {
      if (selector === "a") return children.filter((c) => c.tagName === "A");
      if (selector === "option") return this.options;
      return [];
    },
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] ?? null;
    },
    classList: {
      tokens: new Set(attributes.class ? attributes.class.split(/\s+/) : []),
      contains(name) { return this.tokens.has(name); },
      add(name) { this.tokens.add(name); },
      delete(name) { this.tokens.delete(name); },
      toggle(name, force) {
        const has = this.tokens.has(name);
        const next = force !== undefined ? Boolean(force) : !has;
        if (next) this.tokens.add(name);
        else this.tokens.delete(name);
        return next;
      }
    }
  };
}

function loadSite(browserLanguage = "es", initialPreferences = {}) {
  const textElements = textKeys.map((k) => createElement("span", { "data-i18n": k }));
  const ariaElements = ariaKeys.map((k) => createElement("button", { "data-i18n-aria": k }));
  const altElements = altKeys.map((k) => createElement("img", { "data-i18n-alt": k }));

  const controlIds = [
    "themeToggle", "themeIcon", "languageToggle", "languageLabel",
    "mobileMenuBtn", "mobileMenu", "scenarioSelector", "demoScenarioDesc",
    "demoRequestJson", "demoVerdictBadge", "demoDecisionCode", "demoDecisionReason",
    "demoTraceContainer", "demoAuditJson", "currentYear"
  ];

  const controls = {};
  for (const id of controlIds) {
    const tag = id === "scenarioSelector" ? "select" : "div";
    controls[id] = createElement(tag, { id });
  }

  const metaMap = new Map();
  const rootClasses = new Set();
  const root = {
    lang: "es",
    getAttribute: () => null,
    classList: {
      contains: (name) => rootClasses.has(name),
      add: (name) => rootClasses.add(name),
      delete: (name) => rootClasses.delete(name),
      toggle: (name, force) => {
        const has = rootClasses.has(name);
        const next = force !== undefined ? Boolean(force) : !has;
        if (next) rootClasses.add(name);
        else rootClasses.delete(name);
        return next;
      }
    }
  };

  let domContentLoadedHandler = null;
  const documentMock = {
    documentElement: root,
    title: "",
    addEventListener: (event, handler) => {
      if (event === "DOMContentLoaded") domContentLoadedHandler = handler;
    },
    getElementById: (id) => controls[id] ?? null,
    querySelectorAll: (selector) => {
      if (selector === "[data-i18n]") return textElements;
      if (selector === "[data-i18n-aria]") return ariaElements;
      if (selector === "[data-i18n-alt]") return altElements;
      if (selector === "[data-copy-target]") return [];
      return [];
    },
    querySelector: (selector) => {
      if (!metaMap.has(selector)) metaMap.set(selector, createElement("meta"));
      return metaMap.get(selector);
    },
    createElement: (tag) => createElement(tag)
  };

  const storage = new Map(Object.entries(initialPreferences));
  const localStorageMock = {
    getItem: (key) => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, String(value)),
  };

  const mediaListeners = [];
  const windowMock = {
    matchMedia: (query) => ({
      matches: false,
      addEventListener: (event, cb) => {
        if (event === "change") mediaListeners.push(cb);
      }
    }),
    DEMO_SCENARIOS
  };

  vm.runInNewContext(siteJs, {
    document: documentMock,
    window: windowMock,
    navigator: { language: browserLanguage },
    localStorage: localStorageMock,
    DEMO_SCENARIOS
  }, { filename: "site.js" });

  assert.equal(typeof domContentLoadedHandler, "function", "DOMContentLoaded handler was registered");
  domContentLoadedHandler();

  return {
    document: documentMock,
    controls,
    storage,
    root,
    textElements,
    ariaElements,
    altElements,
    triggerSystemThemeChange: (isDark) => {
      mediaListeners.forEach((cb) => cb({ matches: isDark }));
    }
  };
}

test("all internal anchors resolve to unique existing element IDs", () => {
  for (const page of pages) {
    const ids = [...page.html.matchAll(/\bid="([^"]+)"/g)].map(m => m[1]);
    assert.equal(ids.length, new Set(ids).size, page.file + " has duplicate IDs");
    for (const [, anchor] of page.html.matchAll(/href="#([^"]+)"/g)) {
      assert.ok(ids.includes(anchor), page.file + " missing #" + anchor);
    }
  }
});

test("all new-window links retain safe rel='noopener noreferrer' attributes", () => {
  const targetLinks = [...html.matchAll(/<a\b[^>]*target="_blank"[^>]*>/g)].map((m) => m[0]);
  assert.ok(targetLinks.length > 0, "Expected at least one target='_blank' link");
  for (const link of targetLinks) {
    assert.match(link, /rel="noopener noreferrer"/, `Unsafe target="_blank" link: ${link}`);
  }
});

test("all asset paths use relative URLs for GitHub Pages subpath compatibility", () => {
  for (const [, href] of html.matchAll(/<link\b[^>]*href="([^"]+)"/g)) {
    if (!href.startsWith("http")) {
      assert.ok(!href.startsWith("/"), `Absolute asset link found in <link>: ${href}`);
    }
  }
  for (const [, src] of html.matchAll(/<script\b[^>]*src="([^"]+)"/g)) {
    if (!src.startsWith("http")) {
      assert.ok(!src.startsWith("/"), `Absolute asset script found in <script>: ${src}`);
    }
  }
  for (const [, src] of html.matchAll(/<img\b[^>]*src="([^"]+)"/g)) {
    if (!src.startsWith("http")) {
      assert.ok(!src.startsWith("/"), `Absolute asset img found in <img>: ${src}`);
    }
  }
});

test("every translation key is defined and non-empty in both Spanish and English", () => {
  const esEnv = loadSite("es");
  const enEnv = loadSite("en");

  assert.equal(esEnv.document.documentElement.lang, "es");
  assert.equal(enEnv.document.documentElement.lang, "en");
  assert.match(esEnv.document.title, /Permisos claros/);
  assert.match(enEnv.document.title, /Clear permissions/);

  // Verify all data-i18n keys
  for (const el of esEnv.textElements) {
    const key = el.getAttribute("data-i18n");
    assert.notEqual(el.textContent, "__untranslated__", `Missing ES translation for data-i18n: ${key}`);
    assert.ok(el.textContent.trim().length > 0, `Empty ES translation for data-i18n: ${key}`);
  }

  for (const el of enEnv.textElements) {
    const key = el.getAttribute("data-i18n");
    assert.notEqual(el.textContent, "__untranslated__", `Missing EN translation for data-i18n: ${key}`);
    assert.ok(el.textContent.trim().length > 0, `Empty EN translation for data-i18n: ${key}`);
  }

  // Verify all data-i18n-aria keys
  for (const el of esEnv.ariaElements) {
    const key = el.getAttribute("data-i18n-aria");
    const aria = el.getAttribute("aria-label");
    assert.ok(aria && aria.trim().length > 0, `Missing or empty ES aria-label for key: ${key}`);
  }
  for (const el of enEnv.ariaElements) {
    const key = el.getAttribute("data-i18n-aria");
    const aria = el.getAttribute("aria-label");
    assert.ok(aria && aria.trim().length > 0, `Missing or empty EN aria-label for key: ${key}`);
  }
});

test("Spanish HTML fallback matches translated content even when JavaScript is disabled", () => {
  const source = siteJs.split("  const messages = {")[1].split("\n  };")[0];
  const messages = vm.runInNewContext("({" + source + "})");
  const decode = text => text.replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&#x27;/g, "'").trim();
  for (const {file, html} of pages) {
    for (const match of html.matchAll(/<([a-z][a-z0-9]*)\b[^>]*data-i18n="([^"]+)"[^>]*>([^<]*)<\/\1>/g)) {
      assert.equal(decode(match[3]), messages[match[2]].es, `${file}: ${match[2]}`);
    }
  }
});

test("language switcher toggles ES/EN and persists with mcp_pi_language key", () => {
  const env = loadSite("es");
  assert.equal(env.root.lang, "es");
  assert.equal(env.controls.languageLabel.textContent, "EN");

  // Click toggle -> switches to EN
  env.controls.languageToggle.click();
  assert.equal(env.root.lang, "en");
  assert.equal(env.storage.get("mcp_pi_language"), "en");
  assert.equal(env.controls.languageLabel.textContent, "ES");

  // Click toggle again -> switches back to ES
  env.controls.languageToggle.click();
  assert.equal(env.root.lang, "es");
  assert.equal(env.storage.get("mcp_pi_language"), "es");
  assert.equal(env.controls.languageLabel.textContent, "EN");
});

test("theme switcher toggles Light/Dark and persists with mcp_pi_theme key", () => {
  const env = loadSite("es", { mcp_pi_theme: "light" });
  assert.equal(env.root.classList.contains("dark"), false);

  // Click theme toggle -> dark mode
  env.controls.themeToggle.click();
  assert.equal(env.root.classList.contains("dark"), true);
  assert.equal(env.storage.get("mcp_pi_theme"), "dark");
  assert.equal(env.controls.themeToggle.getAttribute("aria-label"), "Tema: oscuro. Cambiar a sistema");

  // Click theme toggle again -> light mode
  env.controls.themeToggle.click();
  assert.equal(env.root.classList.contains("dark"), false);
  assert.equal(env.storage.get("mcp_pi_theme"), "system");
  assert.equal(env.controls.themeToggle.getAttribute("aria-label"), "Tema: sistema. Cambiar a claro");
});

test("system theme change applies when no explicit mcp_pi_theme is set", () => {
  const env = loadSite("es");
  assert.equal(env.storage.get("mcp_pi_theme") ?? null, null);

  env.triggerSystemThemeChange(true);
  assert.equal(env.root.classList.contains("dark"), true);

  env.triggerSystemThemeChange(false);
  assert.equal(env.root.classList.contains("dark"), false);
});

test("interactive demo scenarios match canonical policy contracts", () => {
  assert.ok(Array.isArray(DEMO_SCENARIOS), "DEMO_SCENARIOS must be an array");
  assert.ok(DEMO_SCENARIOS.length >= 8, "Expected at least 8 educational scenarios");

  const requiredCodes = new Set([
    "ANONYMOUS_CLIENT_DENIED",
    "TOOL_NOT_RECOGNIZED",
    "TOOL_NOT_ALLOWED",
    "WRITES_DISABLED",
    "TARGET_SHELL_DISABLED",
    "PRIVILEGE_GRANT_REQUIRED"
  ]);

  const observedCodes = new Set();

  for (const sc of DEMO_SCENARIOS) {
    assert.ok(sc.id, "Scenario must have an id");
    assert.ok(sc.title_es && sc.title_en, "Scenario must have bilingual titles");
    assert.ok(sc.description_es && sc.description_en, "Scenario must have bilingual descriptions");
    assert.ok(sc.request, "Scenario must have a request object");
    assert.ok(Array.isArray(sc.trace) && sc.trace.length > 0, "Scenario must have a trace array");
    assert.ok(typeof sc.decision.allowed === "boolean", "Decision allowed must be boolean");
    assert.ok(sc.audit_record, "Scenario must have an audit record");

    if (sc.decision.code) {
      observedCodes.add(sc.decision.code);
    }
  }

  for (const code of requiredCodes) {
    assert.ok(observedCodes.has(code), `Expected contract scenario with error code ${code}`);
  }
});

test("scenario selector updates UI and audit display correctly", () => {
  const env = loadSite("es");
  const selector = env.controls.scenarioSelector;
  assert.ok(selector.options.length >= 8, "Selector has options populated");

  // Select anonymous_denied scenario
  selector.value = "anonymous_denied";
  selector.dispatchEvent({ type: "change" });

  assert.equal(env.controls.demoDecisionCode.textContent, "ANONYMOUS_CLIENT_DENIED");
  assert.match(env.controls.demoVerdictBadge.textContent, /COMPROBACIÓN DENEGADA/);
  assert.match(env.controls.demoAuditJson.textContent, /ANONYMOUS_CLIENT_DENIED/);
});

test("all copy buttons reference existing element IDs", () => {
  const ids = new Set([...html.matchAll(/\bid="([^"]+)"/g)].map((m) => m[1]));
  const copyTargets = [...html.matchAll(/data-copy-target="([^"]+)"/g)].map((m) => m[1]);
  assert.ok(copyTargets.length > 0, "Expected at least one copy button");
  for (const target of copyTargets) {
    assert.ok(ids.has(target), `data-copy-target="${target}" does not exist in HTML`);
  }
});

test("permanent simulated notice is displayed in the demo section", () => {
  assert.match(html, /Laboratorio de Simulación Educativo/i);
  assert.match(html, /id="demo"/);
});

test("no private paths, tokens, or credentials leaked in site files", () => {
  assert.doesNotMatch(html, /\/data\/data\/com\.termux/);
  assert.doesNotMatch(siteJs, /\/data\/data\/com\.termux/);
  assert.doesNotMatch(demoJs, /\/data\/data\/com\.termux/);
  assert.doesNotMatch(html, /BEGIN (RSA|OPENSSH|PRIVATE) KEY/);
});

test("accessibility: skip link, landmarks and aria attributes are properly structured", () => {
  assert.match(html, /<a\b[^>]*href="#main-content"[^>]*>/, "Skip link pointing to #main-content is required");
  assert.match(html, /<main\b[^>]*id="main-content"[^>]*>/, "Main landmark with id='main-content' is required");
  assert.match(html, /<header\b/, "Header landmark is present");
  assert.match(html, /<footer\b/, "Footer landmark is present");
  assert.match(html, /<details\b/, "Details accordion is used for FAQ");
});

test("demo decisions and inputs are derived from the actual Go policy contract fixture", () => {
  const contracts = JSON.parse(fs.readFileSync(path.join(siteDir, "../mcp-adapter/testdata/contracts/policy_cases.json"), "utf8"));
  assert.equal(DEMO_SCENARIOS.length, 9);
  for (const scenario of DEMO_SCENARIOS) {
    const cases = contracts[scenario.contract_group];
    assert.ok(cases, `Missing contract group: ${scenario.id}`);
    const source = cases.find(c => c.name === scenario.contract_case);
    assert.ok(source, `Missing contract: ${scenario.id}`);
    assert.equal(scenario.decision.allowed, source.allowed, scenario.id);
    assert.equal(scenario.decision.code, source.error_code, scenario.id);
    assert.equal(JSON.stringify(scenario.request), JSON.stringify(source), scenario.id);
    assert.equal(scenario.audit_record.simulated, true);
    assert.equal(scenario.audit_record.persisted, false);
    assert.equal(scenario.audit_record.sha256, undefined);
  }
});

test("public copy does not invent immutable audits, a license, or SSE transport", () => {
  for (const source of [html, siteJs]) {
    assert.doesNotMatch(source, /inmutable|immutable|incontrovertible|incorruptible|licencia MIT|MIT License|HTTP SSE|Stdio\/SSE/i);
  }
  assert.match(html, /Content-Security-Policy/);
  assert.match(html, /rel="canonical" href="https:\/\/yorologo.github.io\/MCP-Pi\/"/);
});

test("installation checks the published bundle and never invents a desktop client configuration", () => {
  assert.doesNotMatch(html, /latest\/download\/MCP-Pi-linux-armv6|"args": \["--client"/);
  assert.match(html, /MCP-Pi-1\.6\.11-linux-armv6-5172705639d8\.tar\.gz\.sha256/);
  assert.match(html, /sha256sum -c MCP-Pi-1\.6\.11-linux-armv6-5172705639d8\.tar\.gz\.sha256/);
  assert.match(html, /tar -xzf/);
  assert.match(html, /README.md/);
});

test("theme control can return to system mode after a manual selection", () => {
  const env = loadSite("es", { mcp_pi_theme: "dark" });
  env.controls.themeToggle.click();
  assert.equal(env.storage.get("mcp_pi_theme"), "system");
  env.triggerSystemThemeChange(false);
  assert.equal(env.root.classList.contains("dark"), false);
  env.triggerSystemThemeChange(true);
  assert.equal(env.root.classList.contains("dark"), true);
});
