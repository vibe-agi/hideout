#!/usr/bin/env node

import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import net from "node:net";

function arg(name, fallback = "") {
  const idx = process.argv.indexOf(name);
  if (idx < 0) return fallback;
  if (idx + 1 >= process.argv.length) throw new Error(`${name} requires a value`);
  return process.argv[idx + 1];
}

const chromePath = arg("--chrome");
const uiURL = arg("--url");
const baseURL = arg("--base-url");
const token = arg("--token");
const noticeID = arg("--notice-id");
const outDir = arg("--out");
if (!chromePath || !uiURL || !baseURL || !token || !noticeID || !outDir) {
  throw new Error("--chrome, --url, --base-url, --token, --notice-id, and --out are required");
}

function redact(value) {
  if (value == null) return value;
  if (Array.isArray(value)) return value.map(redact);
  if (typeof value === "object") {
    const out = {};
    for (const [k, v] of Object.entries(value)) out[redact(String(k))] = redact(v);
    return out;
  }
  if (typeof value !== "string") return value;
  return value
    .replaceAll(token, "REDACTED")
    .replace(/HIDEOUT_SECRET_[A-Za-z0-9_]+=[^\s,;]*/g, "HIDEOUT_*=REDACTED")
    .replace(/HIDEOUT_SECRET_[A-Za-z0-9_]+/g, "HIDEOUT_*")
    .replace(/setupCredential=[^\s,;]*/g, "setupCredential=REDACTED")
    .replace(/\b(?:cap_|ui_)[0-9a-fA-F]{32,}\b/g, "REDACTED")
    .replace(/\/hostfs-overlay\/objects\/[0-9a-fA-F]{16,}/g, "/hostfs-overlay/objects/REDACTED");
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
    server.on("error", reject);
  });
}

async function waitJSON(url, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  let lastErr;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return await res.json();
      lastErr = new Error(`${url}: ${res.status}`);
    } catch (err) {
      lastErr = err;
    }
    await delay(100);
  }
  throw lastErr || new Error(`timeout waiting for ${url}`);
}

class CDP {
  constructor(ws) {
    this.ws = ws;
    this.nextID = 1;
    this.pending = new Map();
    this.events = [];
    ws.addEventListener("message", (message) => {
      const data = JSON.parse(message.data);
      if (data.id && this.pending.has(data.id)) {
        const { resolve, reject } = this.pending.get(data.id);
        this.pending.delete(data.id);
        if (data.error) reject(new Error(data.error.message || JSON.stringify(data.error)));
        else resolve(data.result || {});
        return;
      }
      if (data.method) this.events.push(data);
    });
  }

  send(method, params = {}) {
    const id = this.nextID++;
    const payload = { id, method, params };
    const promise = new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
    });
    this.ws.send(JSON.stringify(payload));
    return promise;
  }

  eventCount(predicate) {
    return this.events.filter(predicate).length;
  }
}

async function connectTarget(port) {
  const target = await fetch(`http://127.0.0.1:${port}/json/new?${encodeURIComponent("about:blank")}`, {
    method: "PUT"
  }).then((res) => {
    if (!res.ok) throw new Error(`/json/new failed: ${res.status}`);
    return res.json();
  });
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve, { once: true });
    ws.addEventListener("error", reject, { once: true });
  });
  return new CDP(ws);
}

async function evalValue(cdp, expression) {
  const result = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true
  });
  if (result.exceptionDetails) {
    throw new Error(JSON.stringify(result.exceptionDetails));
  }
  return result.result ? result.result.value : undefined;
}

async function waitFor(cdp, expression, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await evalValue(cdp, expression);
    if (last) return last;
    await delay(100);
  }
  throw new Error(`timeout waiting for expression: ${expression}; last=${JSON.stringify(last)}`);
}

function overviewAuditRequest(ev) {
  if (ev.method !== "Network.requestWillBeSent") return false;
  const url = ev.params && ev.params.request && ev.params.request.url || "";
  return url.includes("/api/v1/overview") || url.includes("/api/v1/audit/events");
}

async function main() {
  const port = await freePort();
  const profileDir = await mkdtemp(join(tmpdir(), "hideout-chrome-"));
  const headless = process.env.HIDEOUT_UI_E2E_HEADLESS !== "0";
  const chromeArgs = [
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profileDir}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-background-networking",
    "--disable-sync",
    "--disable-extensions",
    "--disable-component-update",
    "--window-size=1280,900"
  ];
  if (headless) chromeArgs.push("--headless=new", "--disable-gpu", "--hide-scrollbars");
  chromeArgs.push("about:blank");
  const chrome = spawn(chromePath, chromeArgs, { stdio: ["ignore", "pipe", "pipe"] });
  let chromeErr = "";
  chrome.stderr.on("data", (data) => { chromeErr += data.toString(); });
  try {
    await waitJSON(`http://127.0.0.1:${port}/json/version`);
    const cdp = await connectTarget(port);
    await cdp.send("Page.enable");
    await cdp.send("Runtime.enable");
    await cdp.send("Network.enable");
    await cdp.send("Page.addScriptToEvaluateOnNewDocument", {
      source: `
        window.__hideoutProof = { fetches: [] };
        const originalFetch = window.fetch.bind(window);
        window.fetch = async function(input, init) {
          const url = typeof input === "string" ? input : (input && input.url) || "";
          const method = init && init.method || "GET";
          const body = init && init.body || "";
          window.__hideoutProof.fetches.push({url, method, body});
          return originalFetch(input, init);
        };
      `
    });
    await cdp.send("Page.navigate", { url: uiURL });
    await waitFor(cdp, "document.readyState === 'complete'");
    await waitFor(cdp, "document.body && document.body.innerText.includes('Hideout')");
    await waitFor(cdp, "document.body.innerText.includes('Operator Console')");
    await evalValue(cdp, `document.querySelector('[data-panel="operator-console"]').click(); true`);
    await waitFor(cdp, "document.body.innerText.includes('Action Required')");
    const requiredPanels = [
      {label: "Operator Console", any: ["Operator Console"]},
      {label: "Action Required", any: ["Action Required"]},
      {label: "Stream", any: ["Stream"]},
      {label: "Decisions", any: ["Decisions"]},
      {label: "Notices", any: ["Notices"]},
      {label: "HostFS Writes", any: ["HostFS Writes"]},
      {label: "Background", any: ["Background"]},
      {label: "Doctor", any: ["Doctor"]},
      {label: "Package/Support", any: ["Package/Support", "Package", "Support"]},
      {label: "Audit", any: ["Audit"]}
    ];
    const panelsVisible = await evalValue(cdp, `(() => {
      const text = document.body.innerText;
      return ${JSON.stringify(requiredPanels)}.filter((entry) => entry.any.some((name) => text.includes(name))).map((entry) => entry.label);
    })()`);
    if (panelsVisible.length !== requiredPanels.length) {
      throw new Error(`missing visible panels: ${requiredPanels.map((entry) => entry.label).filter((name) => !panelsVisible.includes(name)).join(", ")}`);
    }

    const pollBaseline = cdp.eventCount(overviewAuditRequest);
    await delay(700);
    const idlePolls = cdp.eventCount(overviewAuditRequest) - pollBaseline;
    if (idlePolls !== 0) throw new Error(`hidden polling while stream idle: ${idlePolls}`);

    const beforeEventText = await evalValue(cdp, "document.body.innerText");
    const bgResp = await fetch(`${baseURL}/daemon/background`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json"
      },
      body: JSON.stringify({ op: "environment-clean", ids: [] })
    });
    if (!bgResp.ok) throw new Error(`/daemon/background failed: ${bgResp.status}`);
    await waitFor(cdp, `document.body.innerText.includes('environment-clean') || document.body.innerText !== ${JSON.stringify(beforeEventText)}`, 10000);
    const eventPolls = cdp.eventCount(overviewAuditRequest) - pollBaseline;
    if (eventPolls !== 0) throw new Error(`hidden polling after live event: ${eventPolls}`);

    await evalValue(cdp, `document.querySelector('[data-panel="notices"]').click(); true`);
    await waitFor(cdp, `document.querySelector('button[data-notice-action="ack"][data-notice-id="${noticeID}"]') !== null`);
    const ackBaseline = await evalValue(cdp, "window.__hideoutProof.fetches.length");
    await evalValue(cdp, `document.querySelector('button[data-notice-action="ack"][data-notice-id="${noticeID}"]').click(); true`);
    await waitFor(cdp, `document.body.innerText.includes('acknowledged ${noticeID}') || document.querySelector('button[data-notice-action="ack"][data-notice-id="${noticeID}"]') === null`);
    const fetches = await evalValue(cdp, "window.__hideoutProof.fetches");
    const ackFetches = fetches.slice(ackBaseline);
    const ackFetch = ackFetches.find((entry) => String(entry.url).includes("/api/v1/notice/ack"));
    if (!ackFetch) throw new Error("notice ack request was not observed from browser page");
    if (!String(ackFetch.body || "").includes(noticeID)) throw new Error("notice ack payload did not include notice id");

    await cdp.send("Page.navigate", { url: `${baseURL}/?auth=wrong#token=wrong-token` });
    await waitFor(cdp, "document.readyState === 'complete'");
    await waitFor(cdp, "document.getElementById('status') && document.getElementById('status').className.includes('error') || document.body.innerText.toLowerCase().includes('token')", 10000);

    await cdp.send("Page.navigate", { url: uiURL });
    await waitFor(cdp, "document.readyState === 'complete'");
    await waitFor(cdp, "document.body.innerText.includes('Hideout')");
    const screenshot = await cdp.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await writeFile(join(outDir, "webui-console.png"), Buffer.from(screenshot.data, "base64"));
    const domText = await evalValue(cdp, "document.body.innerText");
    await writeFile(join(outDir, "dom-summary.txt"), redact(domText) + "\n", "utf8");
    const networkSummary = redact(cdp.events.filter((ev) => ev.method === "Network.requestWillBeSent").map((ev) => ({
      url: ev.params.request.url,
      method: ev.params.request.method
    })));
    await writeFile(join(outDir, "network-summary.json"), JSON.stringify(networkSummary, null, 2) + "\n", "utf8");
    const result = {
      panelsVisible,
      liveUpdateObserved: true,
      hiddenPollingDetected: false,
      actionRoundTrip: {
        action: "notice.ack",
        requestObserved: true,
        payloadValidated: true,
        responseHandled: true,
        visibleStateChanged: true
      },
      authFailureObserved: true,
      artifacts: {
        screenshot: "webui-console.png",
        "event-summary": "network-summary.json",
        log: "dom-summary.txt"
      }
    };
    await writeFile(join(outDir, "browser-result.json"), JSON.stringify(redact(result), null, 2) + "\n", "utf8");
  } finally {
    chrome.kill("SIGTERM");
    await new Promise((resolve) => chrome.once("exit", resolve));
    await rm(profileDir, { recursive: true, force: true });
  }
  if (chromeErr.includes("DevToolsActivePort file doesn't exist")) {
    throw new Error(chromeErr);
  }
}

main().catch(async (err) => {
  try {
    await writeFile(join(outDir || ".", "browser-error.log"), redact(err && err.stack || String(err)) + "\n", "utf8");
  } catch {}
  console.error(err && err.stack || err);
  process.exit(1);
});
