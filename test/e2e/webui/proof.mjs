#!/usr/bin/env node

import { spawn } from "node:child_process";
import {
  chmod,
  mkdtemp,
  rm,
  writeFile
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import net from "node:net";

function arg(name, fallback = "") {
  const index = process.argv.indexOf(name);
  if (index < 0) return fallback;
  if (index + 1 >= process.argv.length) {
    throw new Error(`${name} requires a value`);
  }
  return process.argv[index + 1];
}

const chromePath = arg("--chrome");
const uiURL = arg("--url");
const baseURL = arg("--base-url");
let currentToken = arg("--token");
const fixtureURL = arg("--fixture-url");
const fixtureKey = arg("--fixture-key");
const noticeID = arg("--notice-id");
const decisionID = arg("--decision-id");
const sessionID = arg("--session-id");
const executionID = arg("--execution-id");
const filePath = arg("--file-path");
const domain = arg("--domain");
const ip = arg("--ip");
const riskID = arg("--risk-id");
const from = arg("--from");
const to = arg("--to");
const recordCount = Number(arg("--record-count", "0"));
const outDir = arg("--out");
const expectedPanels = Object.freeze([
  "Overview",
  "Timeline",
  "Executions",
  "Files",
  "Network & DNS",
  "Coverage",
  "Risks",
  "Operations",
  "Migration",
  "Configuration",
  "Help"
]);
const expectedRequiredAreas = Object.freeze([
  "Action Required",
  "Stream",
  "Decisions",
  "Notices",
  "HostFS Writes",
  "Background",
  "Doctor",
  "Package/Support",
  "Audit"
]);

if (!chromePath || !uiURL || !baseURL || !currentToken ||
    !fixtureURL || !fixtureKey || !noticeID || !decisionID ||
    !sessionID || !executionID ||
    !filePath || !domain || !ip || !riskID || !from || !to ||
    !Number.isInteger(recordCount) || recordCount <= 200 || !outDir) {
  throw new Error(
    "--chrome, --url, --base-url, --token, --fixture-url, " +
    "--fixture-key, --notice-id, --decision-id, browser evidence identities, " +
    "and --out are required"
  );
}

const sensitiveTokens = new Set([currentToken]);

function redact(value) {
  if (value == null) return value;
  if (Array.isArray(value)) return value.map(redact);
  if (typeof value === "object") {
    const output = {};
    for (const [key, entry] of Object.entries(value)) {
      output[redact(String(key))] = redact(entry);
    }
    return output;
  }
  if (typeof value !== "string") return value;
  let output = value;
  for (const token of sensitiveTokens) {
    if (token) output = output.replaceAll(token, "REDACTED");
  }
  return output
    .replace(/HIDEOUT_SECRET_[A-Za-z0-9_]+=[^\s,;]*/g, "HIDEOUT_*=REDACTED")
    .replace(/HIDEOUT_SECRET_[A-Za-z0-9_]+/g, "HIDEOUT_*")
    .replace(/setupCredential=[^\s,;]*/g, "setupCredential=REDACTED")
    .replace(/\b(?:cap_|ui_)[0-9a-fA-F]{32,}\b/g, "REDACTED")
    .replace(
      /\/hostfs-overlay\/objects\/[0-9a-fA-F]{16,}/g,
      "/hostfs-overlay/objects/REDACTED"
    );
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
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

async function waitJSON(url, timeoutMS = 10000) {
  const deadline = Date.now() + timeoutMS;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return await response.json();
      lastError = new Error(`${url}: ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await delay(100);
  }
  throw lastError || new Error(`timeout waiting for ${url}`);
}

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 1;
    this.pending = new Map();
    this.events = [];
    socket.addEventListener("message", (message) => {
      const data = JSON.parse(message.data);
      if (data.id && this.pending.has(data.id)) {
        const { resolve, reject } = this.pending.get(data.id);
        this.pending.delete(data.id);
        if (data.error) {
          reject(new Error(data.error.message || JSON.stringify(data.error)));
        } else {
          resolve(data.result || {});
        }
        return;
      }
      if (data.method) this.events.push(data);
    });
  }

  send(method, params = {}) {
    const id = this.nextID++;
    const promise = new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
    });
    this.socket.send(JSON.stringify({id, method, params}));
    return promise;
  }
}

async function connectTarget(port) {
  const target = await fetch(
    `http://127.0.0.1:${port}/json/new?${encodeURIComponent("about:blank")}`,
    {method: "PUT"}
  ).then((response) => {
    if (!response.ok) {
      throw new Error(`/json/new failed: ${response.status}`);
    }
    return response.json();
  });
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, {once: true});
    socket.addEventListener("error", reject, {once: true});
  });
  return new CDP(socket);
}

async function evalValue(cdp, expression) {
  const result = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true
  });
  if (result.exceptionDetails) {
    throw new Error(
      `browser expression failed: ${JSON.stringify(result.exceptionDetails)}`
    );
  }
  return result.result ? result.result.value : undefined;
}

async function waitFor(cdp, expression, timeoutMS = 10000) {
  const deadline = Date.now() + timeoutMS;
  let last;
  while (Date.now() < deadline) {
    last = await evalValue(cdp, expression);
    if (last) return last;
    await delay(50);
  }
  throw new Error(
    `timeout waiting for expression: ${expression}; ` +
    `last=${JSON.stringify(last)}`
  );
}

async function fixturePost(path) {
  const response = await fetch(`${fixtureURL}${path}`, {
    method: "POST",
    headers: {"X-Hideout-E2E-Key": fixtureKey}
  });
  if (!response.ok) {
    throw new Error(`fixture ${path} failed: ${response.status}`);
  }
  if ((response.headers.get("content-type") || "").includes("json")) {
    return response.json();
  }
  return null;
}

async function managerJSON(path, token = currentToken) {
  const response = await fetch(`${baseURL}${path}`, {
    headers: {"X-Hideout-UI-Token": token},
    cache: "no-store"
  });
  const body = await response.text();
  let envelope;
  try {
    envelope = JSON.parse(body);
  } catch {
    throw new Error(`Manager ${path} returned non-JSON status ${response.status}`);
  }
  if (!response.ok) {
    throw new Error(
      `Manager ${path} failed: ${response.status} ${JSON.stringify(envelope)}`
    );
  }
  return envelope;
}

function browserRequestCount(cdp, predicate) {
  return cdp.events.filter((event) => {
    if (event.method !== "Network.requestWillBeSent") return false;
    const request = event.params && event.params.request;
    return request && predicate(request.url, request.method);
  }).length;
}

async function waitForBrowserRequestIncrease(
  cdp,
  predicate,
  baseline,
  timeoutMS = 10000
) {
  const deadline = Date.now() + timeoutMS;
  let observed = browserRequestCount(cdp, predicate);
  while (Date.now() < deadline) {
    observed = browserRequestCount(cdp, predicate);
    if (observed > baseline) return observed;
    await delay(50);
  }
  throw new Error(
    "timeout waiting for browser request count to increase: " +
    `baseline=${baseline} observed=${observed}`
  );
}

async function secureWrite(name, data, encoding) {
  const path = join(outDir, name);
  if (encoding) await writeFile(path, data, encoding);
  else await writeFile(path, data);
  await chmod(path, 0o600);
}

async function capture(cdp, name) {
  const screenshot = await cdp.send("Page.captureScreenshot", {
    format: "png",
    captureBeyondViewport: false
  });
  await secureWrite(name, Buffer.from(screenshot.data, "base64"));
}

async function selectTab(cdp, id, expectedText = "") {
  await evalValue(cdp, `(() => {
    const tab = document.getElementById(${JSON.stringify(id)});
    if (!tab) throw new Error("tab is missing");
    tab.click();
    return true;
  })()`);
  await waitFor(
    cdp,
    `document.getElementById(${JSON.stringify(id)}).getAttribute("aria-selected") === "true"`
  );
  if (expectedText) {
    await waitFor(
      cdp,
      `document.body.innerText.includes(${JSON.stringify(expectedText)})`
    );
  }
}

async function applyFilters(cdp, values, expectedText = "") {
  const before = await evalValue(
    cdp,
    `window.__hideoutProof.fetches.filter(
      (entry) => String(entry.url).includes("/api/v1/activity/events")
    ).length`
  );
  const started = performance.now();
  await evalValue(cdp, `(() => {
    const ids = {
      kinds:"filterKinds",
      path:"filterPath",
      domain:"filterDomain",
      ip:"filterIP",
      operations:"filterOperations",
      executions:"filterExecutions",
      risks:"filterRisks",
      from:"filterFrom",
      to:"filterTo"
    };
    const values = ${JSON.stringify(values)};
    for (const id of Object.values(ids)) {
      document.getElementById(id).value = "";
    }
    const localDateTime = (raw) => {
      const date = new Date(raw);
      const local = new Date(
        date.getTime() - date.getTimezoneOffset() * 60000
      );
      return local.toISOString().slice(0, 19);
    };
    for (const [name, value] of Object.entries(values)) {
      const input = document.getElementById(ids[name]);
      input.value = (name === "from" || name === "to") ?
        localDateTime(value) : value;
    }
    document.getElementById("activityFilters").requestSubmit();
    return true;
  })()`);
  await waitFor(
    cdp,
    `window.__hideoutProof.fetches.filter(
      (entry) => String(entry.url).includes("/api/v1/activity/events")
    ).length > ${before}`
  );
  await waitFor(
    cdp,
    `document.getElementById("timelineBody").getAttribute("aria-busy") === "false"`
  );
  if (expectedText) {
    await waitFor(
      cdp,
      `document.getElementById("timelineBody").innerText.includes(
        ${JSON.stringify(expectedText)}
      )`
    );
  }
  const query = await evalValue(cdp, `(() => {
    const entries = window.__hideoutProof.fetches.filter(
      (entry) => String(entry.url).includes("/api/v1/activity/events")
    );
    return entries[entries.length - 1].url;
  })()`);
  return {elapsedMS: performance.now() - started, query};
}

function auditExpression() {
  return `(() => {
    const violations = [];
    const seen = new Set();
    for (const node of document.querySelectorAll("[id]")) {
      if (seen.has(node.id)) violations.push("duplicate-id:" + node.id);
      seen.add(node.id);
    }
    const tabs = Array.from(document.querySelectorAll('[role="tab"]'));
    const selected = tabs.filter(
      (tab) => tab.getAttribute("aria-selected") === "true"
    );
    if (tabs.length !== ${expectedPanels.length}) {
      violations.push("tab-count:" + tabs.length);
    }
    if (selected.length !== 1) {
      violations.push("selected-tab-count:" + selected.length);
    }
    for (const tab of tabs) {
      const panel = document.getElementById(
        tab.getAttribute("aria-controls") || ""
      );
      if (!panel || panel.getAttribute("role") !== "tabpanel") {
        violations.push("tab-target:" + tab.id);
      }
      if (!tab.textContent.trim()) violations.push("tab-name:" + tab.id);
    }
    for (const panel of document.querySelectorAll('[role="tabpanel"]')) {
      const tab = document.getElementById(
        panel.getAttribute("aria-labelledby") || ""
      );
      if (!tab || tab.getAttribute("role") !== "tab") {
        violations.push("tabpanel-label:" + panel.id);
      }
    }
    for (const node of document.querySelectorAll("[aria-labelledby]")) {
      for (const id of node.getAttribute("aria-labelledby").split(/\\s+/)) {
        if (id && !document.getElementById(id)) {
          violations.push("missing-labelledby:" + id);
        }
      }
    }
    const controls = Array.from(
      document.querySelectorAll("button,input,select,textarea")
    ).filter((node) => {
      if (node.closest("[hidden]")) return false;
      const dialog = node.closest("dialog");
      return !dialog || dialog.open;
    });
    for (const control of controls) {
      const name = (
        control.getAttribute("aria-label") ||
        (control.labels && Array.from(control.labels).map(
          (label) => label.textContent
        ).join(" ")) ||
        control.textContent ||
        ""
      ).trim();
      if (!name) {
        violations.push(
          "unnamed-control:" + (control.id || control.tagName.toLowerCase())
        );
      }
    }
    if (!document.querySelector(".skip-link")) {
      violations.push("skip-link-missing");
    }
    if (!document.getElementById("consoleAnnouncement")) {
      violations.push("live-region-missing");
    }
    return Array.from(new Set(violations)).sort();
  })()`;
}

async function stopChrome(chrome) {
  if (chrome.exitCode !== null) return;
  chrome.kill("SIGTERM");
  const exited = new Promise((resolve) => chrome.once("exit", resolve));
  await Promise.race([exited, delay(3000)]);
  if (chrome.exitCode === null) {
    chrome.kill("SIGKILL");
    await Promise.race([exited, delay(1000)]);
  }
}

async function main() {
  const port = await freePort();
  const profileDir = await mkdtemp(join(tmpdir(), "hideout-chrome-"));
  const headless = process.env.HIDEOUT_UI_E2E_HEADLESS !== "0";
  const chromeArguments = [
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profileDir}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-background-networking",
    "--disable-sync",
    "--disable-extensions",
    "--disable-component-update",
    "--disable-features=Translate",
    "--window-size=1280,900"
  ];
  if (headless) {
    chromeArguments.push(
      "--headless=new",
      "--disable-gpu",
      "--hide-scrollbars"
    );
  }
  chromeArguments.push("about:blank");
  const chrome = spawn(chromePath, chromeArguments, {
    stdio: ["ignore", "pipe", "pipe"]
  });
  let chromeError = "";
  chrome.stderr.on("data", (data) => {
    chromeError += data.toString();
  });

  try {
    await waitJSON(`http://127.0.0.1:${port}/json/version`);
    const cdp = await connectTarget(port);
    await cdp.send("Page.enable");
    await cdp.send("Runtime.enable");
    await cdp.send("Network.enable");
    await cdp.send("Performance.enable");
    await cdp.send("Page.addScriptToEvaluateOnNewDocument", {
      source: `
        window.__hideoutProof = {
          fetches: [],
          blockSnapshots: false,
          delayDecisionInspect: false,
          returnButton: null
        };
        const originalFetch = window.fetch.bind(window);
        window.fetch = async function(input, init) {
          const url = typeof input === "string" ?
            input : (input && input.url) || "";
          const method = init && init.method || "GET";
          const body = init && init.body || "";
          window.__hideoutProof.fetches.push({url, method, body});
          if (window.__hideoutProof.blockSnapshots &&
              String(url).includes("/api/v1/operator/snapshot")) {
            throw new Error("injected browser snapshot transport failure");
          }
          const response = await originalFetch(input, init);
          if (window.__hideoutProof.delayDecisionInspect &&
              method === "GET" &&
              String(url).includes("/api/v1/decisions/")) {
            await new Promise((resolve) => setTimeout(resolve, 250));
          }
          return response;
        };
      `
    });

    const navigationStarted = performance.now();
    await cdp.send("Page.navigate", {url: uiURL});
    await waitFor(cdp, "document.readyState === 'complete'");
    try {
      await waitFor(
        cdp,
        `document.getElementById("connectionState").textContent.trim() === "LIVE"`,
        15000
      );
    } catch (error) {
      const diagnostic = await evalValue(cdp, `(() => ({
        state:document.getElementById("connectionState") &&
          document.getElementById("connectionState").textContent,
        reason:document.getElementById("connectionReason") &&
          document.getElementById("connectionReason").textContent,
        banner:document.getElementById("staleBanner") &&
          document.getElementById("staleBanner").textContent,
        body:document.body && document.body.innerText.slice(0, 4000),
        hash:location.hash
      }))()`);
      let snapshotShape = {};
      try {
        const envelope = await managerJSON(
          "/api/v1/operator/snapshot?activityLimit=100"
        );
        const snapshot = envelope.data || {};
        snapshotShape = {
          resource: envelope.resource,
          keys: Object.keys(snapshot).sort(),
          arrays: Object.fromEntries([
            "profiles", "sessions", "environments", "activity", "coverage",
            "risks", "operations", "capabilities", "nextActions"
          ].map((name) => [
            name,
            {
              array: Array.isArray(snapshot[name]),
              type: typeof snapshot[name],
              length: Array.isArray(snapshot[name]) ?
                snapshot[name].length : -1
            }
          ]))
        };
      } catch (snapshotError) {
        snapshotShape = {error: String(snapshotError)};
      }
      const exceptions = cdp.events
        .filter((event) => event.method === "Runtime.exceptionThrown")
        .map((event) => event.params && event.params.exceptionDetails);
      throw new Error(
        `initial console did not become live: ${String(error)} ` +
        `diagnostic=${JSON.stringify(diagnostic)} ` +
        `snapshotShape=${JSON.stringify(snapshotShape)} ` +
        `exceptions=${JSON.stringify(exceptions)}`
      );
    }
    await waitFor(
      cdp,
      `document.getElementById("sessionScope").value === ${JSON.stringify(sessionID)}`
    );
    const loadToLiveMS = performance.now() - navigationStarted;
    const credentialHygiene = await evalValue(cdp, `(() => ({
      fragment:location.hash,
      local:Object.keys(localStorage),
      session:Object.keys(sessionStorage),
      cookies:document.cookie
    }))()`);
    if (credentialHygiene.fragment ||
        credentialHygiene.local.length ||
        credentialHygiene.session.length ||
        credentialHygiene.cookies) {
      throw new Error(
        `browser credential persisted: ${JSON.stringify(credentialHygiene)}`
      );
    }

    const panelsVisible = await evalValue(cdp, `Array.from(
      document.querySelectorAll('[role="tab"]')
    ).map((tab) => tab.textContent.trim())`);
    if (JSON.stringify(panelsVisible) !== JSON.stringify(expectedPanels)) {
      throw new Error(
        `console panels mismatch: ${JSON.stringify(panelsVisible)}`
      );
    }
    const requiredAreasVisible = await evalValue(cdp, `Array.from(
      document.querySelectorAll('#overviewBody [data-overview-area]')
    ).filter((node) => {
      const style = getComputedStyle(node);
      const bounds = node.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" &&
        bounds.width > 0 && bounds.height > 0;
    }).map((node) => node.dataset.overviewArea)`);
    if (JSON.stringify(requiredAreasVisible) !==
        JSON.stringify(expectedRequiredAreas)) {
      throw new Error(
        `required console areas mismatch: ` +
        `${JSON.stringify(requiredAreasVisible)}`
      );
    }
    await capture(cdp, "webui-overview.png");

    await waitFor(
      cdp,
      `document.querySelector(
        '[data-action="review-decision"][data-decision-id="' +
        ${JSON.stringify(decisionID)} + '"]'
      ) !== null`
    );
    await evalValue(cdp, `(() => {
      const button = document.querySelector(
        '[data-action="review-decision"][data-decision-id="' +
        ${JSON.stringify(decisionID)} + '"]'
      );
      if (!button) throw new Error("visible decision review is unavailable");
      button.click();
      return true;
    })()`);
    const decisionReviewVisible = Boolean(await waitFor(
      cdp,
      `document.getElementById("consoleDialog").open &&
       document.getElementById("dialogBody").innerText.includes(
         ${JSON.stringify(decisionID)}
       ) &&
       document.querySelector('[data-action="claim-decision"]') !== null`
    ));
    await evalValue(cdp, `document.getElementById("dialogClose").click(); true`);
    await waitFor(cdp, `!document.getElementById("consoleDialog").open`);

    await evalValue(cdp, `(() => {
      window.__hideoutProof.delayDecisionInspect = true;
      const button = document.querySelector(
        '[data-action="review-decision"][data-decision-id="' +
        ${JSON.stringify(decisionID)} + '"]'
      );
      button.click();
      document.getElementById("dialogClose").click();
      return true;
    })()`);
    await delay(400);
    const staleDecisionSuppressed = Boolean(await evalValue(cdp, `(() => {
      window.__hideoutProof.delayDecisionInspect = false;
      return !document.getElementById("consoleDialog").open;
    })()`));
    if (!staleDecisionSuppressed) {
      throw new Error("closed decision review reopened from a stale response");
    }

    await waitFor(
      cdp,
      `document.querySelector(
        '[data-action="ack-notice"][data-notice-id="' +
        ${JSON.stringify(noticeID)} + '"]'
      ) !== null`
    );
    const noticeBefore = await managerJSON(
      `/api/v1/notices/${encodeURIComponent(noticeID)}`
    );
    await evalValue(cdp, `(() => {
      const button = document.querySelector(
        '[data-action="ack-notice"][data-notice-id="' +
        ${JSON.stringify(noticeID)} + '"]'
      );
      if (!button || button.disabled) {
        throw new Error("visible notice acknowledgement is unavailable");
      }
      button.click();
      return true;
    })()`);
    const noticeVisibleStateChanged = Boolean(await waitFor(
      cdp,
      `!document.querySelector(
        '[data-action="ack-notice"][data-notice-id="' +
        ${JSON.stringify(noticeID)} + '"]'
      ) && document.getElementById("overviewBody").innerText.includes(
        ${JSON.stringify(`Acknowledged ${noticeID}.`)}
      )`
    ));
    const noticeAfter = await managerJSON(
      `/api/v1/notices/${encodeURIComponent(noticeID)}`
    );
    const noticeFetches = await evalValue(cdp, "window.__hideoutProof.fetches");
    const noticeFetch = noticeFetches.find((entry) =>
      String(entry.url).includes(
        `/api/v1/notices/${encodeURIComponent(noticeID)}/ack`
      )
    );
    let noticePayload = {};
    try {
      noticePayload = JSON.parse(noticeFetch && noticeFetch.body || "{}");
    } catch {
      noticePayload = {};
    }
    const noticeAcknowledgement = {
      noticeId: noticeID,
      requestObserved: Boolean(noticeFetch && noticeFetch.method === "POST"),
      payloadValidated: noticePayload.noticeId === noticeID &&
        noticePayload.surface === "webui",
      responseHandled: Boolean(
        noticeBefore.data && noticeBefore.data.acknowledged === false &&
        noticeAfter.data && noticeAfter.data.acknowledged === true
      ),
      visibleStateChanged: noticeVisibleStateChanged
    };
    if (!Object.values(noticeAcknowledgement).every((value) =>
      typeof value === "string" ? value.length > 0 : value === true
    )) {
      throw new Error(
        `notice acknowledgement incomplete: ` +
        `${JSON.stringify(noticeAcknowledgement)}`
      );
    }

    await evalValue(cdp, `(() => {
      const tab = document.getElementById("tab-overview");
      tab.focus();
      tab.dispatchEvent(new KeyboardEvent("keydown", {
        key:"ArrowRight",
        bubbles:true
      }));
      return true;
    })()`);
    const keyboardNavigation = Boolean(await waitFor(
      cdp,
      `document.activeElement === document.getElementById("tab-timeline") &&
       document.getElementById("tab-timeline").getAttribute(
         "aria-selected"
       ) === "true"`
    ));

    await waitFor(
      cdp,
      `document.getElementById("timelineBody").innerText.includes(
        ${JSON.stringify(filePath)}
      )`,
      15000
    );
    const initialReadBaseline = browserRequestCount(
      cdp,
      (url) => url.includes("/api/v1/operator/snapshot") ||
        url.includes("/api/v1/activity/")
    );
    await delay(2300);
    const idleReadCount = browserRequestCount(
      cdp,
      (url) => url.includes("/api/v1/operator/snapshot") ||
        url.includes("/api/v1/activity/")
    );
    const hiddenPollingDetected = idleReadCount !== initialReadBaseline;
    if (hiddenPollingDetected) {
      throw new Error(
        `healthy SSE performed hidden polling: ` +
        `${initialReadBaseline}->${idleReadCount}`
      );
    }

    await selectTab(cdp, "tab-executions", executionID);
    const executionFacts = await evalValue(cdp, `(() => {
      const text = document.getElementById("executionsBody").innerText;
      return {
        tree:text.includes("/usr/local/bin/claude") &&
          text.includes("/usr/bin/curl"),
        identity:text.includes("developer") && text.includes("1000")
      };
    })()`);
    await selectTab(cdp, "tab-files", filePath);
    await selectTab(cdp, "tab-network", domain);
    const networkFacts = await evalValue(
      cdp,
      `document.getElementById("networkBody").innerText.includes(
        ${JSON.stringify(ip)}
      )`
    );
    await selectTab(cdp, "tab-coverage", "encrypted-dns-unobserved");
    await selectTab(
      cdp,
      "tab-risks",
      "File changed outside the workspace"
    );
    await selectTab(cdp, "tab-timeline", filePath);

    const correlation = await evalValue(cdp, `(() => {
      const row = Array.from(
        document.querySelectorAll("#timelineBody .row")
      ).find((candidate) => candidate.innerText.includes(
        ${JSON.stringify(filePath)}
      ));
      if (!row) return false;
      const button = row.querySelector("button");
      window.__hideoutProof.returnButton = button;
      button.focus();
      button.click();
      return true;
    })()`);
    if (!correlation) throw new Error("correlated activity row is missing");
    await waitFor(
      cdp,
      `document.getElementById("consoleDialog").open &&
       document.activeElement === document.getElementById("dialogTitle") &&
       document.getElementById("dialogBody").innerText.includes(
         ${JSON.stringify(executionID)}
       )`
    );
    await evalValue(cdp, `document.getElementById("dialogClose").click(); true`);
    const focusReturned = Boolean(await waitFor(
      cdp,
      `document.activeElement === window.__hideoutProof.returnButton`
    ));

    const filterRuns = [
      {
        name: "kind",
        values: {kinds: "dns"},
        expected: domain,
        query: "kind=dns"
      },
      {
        name: "operation",
        values: {operations: "connect"},
        expected: domain,
        query: "operation=connect"
      },
      {
        name: "execution",
        values: {executions: executionID},
        expected: executionID,
        query: "execution="
      },
      {
        name: "path",
        values: {path: filePath},
        expected: filePath,
        query: "path="
      },
      {
        name: "domain",
        values: {domain},
        expected: domain,
        query: "domain="
      },
      {
        name: "ip",
        values: {ip},
        expected: ip,
        query: "ip="
      },
      {
        name: "risk-and-time",
        values: {risks: riskID, from, to},
        expected: filePath,
        query: "risk="
      }
    ];
    const filtersExercised = [];
    let maxFilterMS = 0;
    for (const filter of filterRuns) {
      const result = await applyFilters(
        cdp,
        filter.values,
        filter.expected
      );
      maxFilterMS = Math.max(maxFilterMS, result.elapsedMS);
      if (!String(result.query).includes(filter.query)) {
        throw new Error(
          `filter ${filter.name} did not reach Manager: ${result.query}`
        );
      }
      if (filter.name === "risk-and-time" &&
          (!String(result.query).includes("from=") ||
           !String(result.query).includes("to="))) {
        throw new Error(`time filters did not reach Manager: ${result.query}`);
      }
      filtersExercised.push(filter.name);
    }
    await applyFilters(cdp, {}, filePath);
    const mountedBeforeLive = await evalValue(cdp, `(() => {
      const bodies = Array.from(document.querySelectorAll(".history-body"));
      const mounted = bodies.map((body) => {
        const stack = body.querySelector(":scope > .stack");
        return stack ? stack.children.length :
          body.querySelectorAll(":scope > *").length;
      });
      return {
        maximum:Math.max(...mounted),
        timeline:document.querySelectorAll("#timelineBody .row").length,
        notice:Boolean(document.querySelector("#timelineBody .dom-limit"))
      };
    })()`);
    const boundedDOM = mountedBeforeLive.maximum <= 200 &&
      mountedBeforeLive.timeline <= 200 &&
      mountedBeforeLive.notice;
    if (!boundedDOM) {
      throw new Error(
        `history DOM is unbounded: ${JSON.stringify(mountedBeforeLive)}`
      );
    }

    const detailBaseline = browserRequestCount(
      cdp,
      (url) => url.includes("/api/v1/activity/")
    );
    const snapshotBaseline = browserRequestCount(
      cdp,
      (url) => url.includes("/api/v1/operator/snapshot")
    );
    const liveStarted = performance.now();
    await fixturePost("/browser-console/live");
    await waitFor(
      cdp,
      `document.getElementById("timelineBody").innerText.includes(
        "/workspace/live-update.txt"
      )`,
      10000
    );
    const liveUpdateMS = performance.now() - liveStarted;
    const detailAfterLive = await waitForBrowserRequestIncrease(
      cdp,
      (url) => url.includes("/api/v1/activity/"),
      detailBaseline
    );
    await waitFor(
      cdp,
      `document.getElementById("timelineBody").getAttribute("aria-busy") === "false"`
    );
    const snapshotAfterLive = browserRequestCount(
      cdp,
      (url) => url.includes("/api/v1/operator/snapshot")
    );
    const liveUpdateObserved = detailAfterLive > detailBaseline &&
      snapshotAfterLive === snapshotBaseline;
    if (!liveUpdateObserved) {
      const snapshotRequests = redact(cdp.events
        .filter((event) => event.method === "Network.requestWillBeSent" &&
          event.params && event.params.request &&
          event.params.request.url.includes("/api/v1/operator/snapshot"))
        .map((event) => ({
          requestId:event.params.requestId,
          loaderId:event.params.loaderId,
          timestamp:event.params.timestamp,
          wallTime:event.params.wallTime,
          url:event.params.request.url,
          initiator:event.params.initiator
        })));
      await secureWrite(
        "live-update-failure.json",
        JSON.stringify({snapshotRequests}, null, 2) + "\n",
        "utf8"
      );
      throw new Error(
        "live event did not refresh detail through SSE-only orchestration: " +
        `detail=${detailBaseline}->${detailAfterLive} ` +
        `snapshot=${snapshotBaseline}->${snapshotAfterLive} ` +
        `snapshotRequests=${JSON.stringify(snapshotRequests)}`
      );
    }

    const beforeProjection = await managerJSON(
      "/api/v1/profiles/default/projection"
    );
    const beforeRevision = beforeProjection.data.revision;
    await selectTab(cdp, "tab-config", "Activity retention");
    const configurationStarted = performance.now();
    await evalValue(cdp, `(() => {
      const row = Array.from(
        document.querySelectorAll("#configBody .row")
      ).find((candidate) =>
        candidate.querySelector(".row-title") &&
        candidate.querySelector(".row-title").textContent.trim() ===
          "Activity retention"
      );
      const button = row && row.querySelector("button");
      if (!button || button.disabled) {
        throw new Error("activity retention editor is unavailable");
      }
      button.click();
      return true;
    })()`);
    await waitFor(
      cdp,
      `document.getElementById("consoleDialog").open &&
       document.getElementById("dialogTitle").textContent.includes(
         "Activity retention"
       )`
    );
    await evalValue(cdp, `(() => {
      const bytes = document.querySelector(
        '#dialogBody [data-config-field="maxBytes"]'
      );
      const age = document.querySelector(
        '#dialogBody [data-config-field="maxAgeSeconds"]'
      );
      bytes.value = String(Number(bytes.value) + 4096);
      age.value = "0";
      const add = Array.from(
        document.querySelectorAll("#dialogBody button")
      ).find((button) => button.textContent === "Add to local draft");
      add.click();
      return true;
    })()`);
    await waitFor(
      cdp,
      `!document.getElementById("consoleDialog").open &&
       document.getElementById("configBody").innerText.includes(
         "Client-local configuration draft"
       )`
    );
    await evalValue(cdp, `(() => {
      const button = document.querySelector(
        '#configBody [data-action="review-config-draft"]'
      );
      if (!button || button.disabled) {
        throw new Error("configuration draft review is unavailable");
      }
      button.click();
      return true;
    })()`);
    try {
      await waitFor(
        cdp,
        `document.getElementById("consoleDialog").open &&
         document.getElementById("dialogTitle").textContent ===
           "Review configuration plan"`
      );
    } catch (error) {
      const diagnostic = await evalValue(cdp, `(() => ({
        dialogOpen:document.getElementById("consoleDialog").open,
        dialogTitle:document.getElementById("dialogTitle").textContent,
        config:document.getElementById("configBody").innerText,
        lastRequests:window.__hideoutProof.fetches.slice(-5)
      }))()`);
      throw new Error(
        `configuration review did not open: ${String(error)} ` +
        `diagnostic=${JSON.stringify(diagnostic)}`
      );
    }
    await evalValue(cdp, `(() => {
      const button = Array.from(
        document.querySelectorAll("#dialogBody button")
      ).find((candidate) => candidate.textContent === "Choose Apply");
      if (!button || button.disabled) {
        throw new Error("reviewed configuration plan is not confirmable");
      }
      button.click();
      return true;
    })()`);
    await waitFor(
      cdp,
      `document.getElementById("dialogTitle").textContent ===
         "Confirm configuration"`
    );
    await evalValue(cdp, `(() => {
      const checkbox = document.getElementById(
        "configExplicitConfirmation"
      );
      checkbox.checked = true;
      checkbox.dispatchEvent(new Event("change", {bubbles:true}));
      const button = Array.from(
        document.querySelectorAll("#dialogBody button")
      ).find((candidate) =>
        candidate.textContent === "Confirm and apply exact plan"
      );
      if (button.disabled) {
        throw new Error("explicit confirmation did not enable exact apply");
      }
      button.click();
      return true;
    })()`);
    await waitFor(
      cdp,
      `document.getElementById("consoleDialog").open &&
       document.getElementById("dialogTitle").textContent ===
         "Configuration outcome · succeeded"`,
      15000
    );
    const configurationMS = performance.now() - configurationStarted;
    const terminalText = await evalValue(
      cdp,
      `document.getElementById("dialogBody").innerText`
    );
    const fetches = await evalValue(cdp, "window.__hideoutProof.fetches");
    const planFetch = fetches.find((entry) =>
      String(entry.url).includes("/api/v1/profile/transaction/plan")
    );
    const applyFetch = fetches.find((entry) =>
      String(entry.url).includes("/api/v1/profile/transaction/apply")
    );
    let applyPayload = {};
    try {
      applyPayload = JSON.parse(applyFetch && applyFetch.body || "{}");
    } catch {
      applyPayload = {};
    }
    const operationID = String(applyPayload.operationId || "");
    const payloadValidated = Boolean(
      applyPayload.confirmed === true &&
      /^op_[A-Za-z0-9_-]{8,124}$/.test(operationID) &&
      typeof applyPayload.planDigest === "string" &&
      applyPayload.planDigest.length > 0
    );
    const afterProjection = await managerJSON(
      "/api/v1/profiles/default/projection"
    );
    const revisionAdvanced =
      afterProjection.data.revision === beforeRevision + 1;
    const actionRoundTrip = {
      action: "profile.transaction.apply",
      requestObserved: Boolean(planFetch && applyFetch),
      payloadValidated,
      responseHandled: terminalText.includes(operationID) &&
        terminalText.includes("durable terminal proof") &&
        terminalText.includes("yes"),
      visibleStateChanged: revisionAdvanced,
      operationId: operationID,
      revisionAdvanced,
      terminalPhase: "succeeded"
    };
    if (!Object.values(actionRoundTrip).every((value) =>
      typeof value === "string" ? value.length > 0 : value === true
    )) {
      throw new Error(
        `configuration round trip incomplete: ` +
        `${JSON.stringify(actionRoundTrip)}`
      );
    }
    await evalValue(cdp, `(() => {
      const done = Array.from(
        document.querySelectorAll("#dialogBody button")
      ).find((button) => button.textContent === "Done");
      done.click();
      return true;
    })()`);

    const accessibilityViolations = await evalValue(
      cdp,
      auditExpression()
    );
    await secureWrite(
      "accessibility.json",
      JSON.stringify(redact({
        violations: accessibilityViolations,
        tabCount: panelsVisible.length,
        dialogFocusReturned: focusReturned
      }), null, 2) + "\n",
      "utf8"
    );
    if (accessibilityViolations.length || !focusReturned) {
      throw new Error(
        `accessibility audit failed: ` +
        `${JSON.stringify(accessibilityViolations)} ` +
        `focusReturned=${focusReturned}`
      );
    }

    await selectTab(cdp, "tab-config", "Activity retention");
    await evalValue(
      cdp,
      "window.__hideoutProof.blockSnapshots = true; true"
    );
    await fixturePost("/browser-console/gap");
    const staleReadOnlyObserved = Boolean(await waitFor(
      cdp,
      `document.getElementById("connectionState").textContent.trim() ===
         "STALE" &&
       Array.from(document.querySelectorAll("#configBody .row")).some(
         (row) => row.innerText.includes("Activity retention") &&
           row.querySelector("button") &&
           row.querySelector("button").disabled
       )`,
      3000
    ));
    await capture(cdp, "webui-stale.png");
    await evalValue(cdp, `(() => {
      window.__hideoutProof.blockSnapshots = false;
      document.getElementById("reseed").click();
      return true;
    })()`);
    await waitFor(
      cdp,
      `document.getElementById("connectionState").textContent.trim() === "LIVE"`,
      10000
    );

    const rotated = await fixturePost("/browser-console/rotate");
    if (!rotated || !rotated.token) {
      throw new Error("credential rotation did not return a fresh link token");
    }
    sensitiveTokens.add(rotated.token);
    await waitFor(
      cdp,
      `document.getElementById("connectionState").textContent.trim() ===
        "CREDENTIAL-EXPIRED"`,
      5000
    );
    await evalValue(
      cdp,
      `location.hash = "#token=" + ${JSON.stringify(rotated.token)}; true`
    );
    await waitFor(
      cdp,
      `document.getElementById("connectionState").textContent.trim() ===
         "LIVE" && location.hash === ""`,
      10000
    );
    currentToken = rotated.token;
    const credentialRefreshObserved = true;

    const wrongToken = "ui_" + "c".repeat(48);
    sensitiveTokens.add(wrongToken);
    await cdp.send("Page.navigate", {
      url: `${baseURL}/#token=${wrongToken}`
    });
    await waitFor(cdp, "document.readyState === 'complete'");
    const authFailureObserved = Boolean(await waitFor(
      cdp,
      `document.getElementById("connectionState").textContent.trim() ===
         "CREDENTIAL-EXPIRED" &&
       location.hash === "" &&
       document.getElementById("staleBanner").innerText.includes(
         "Credential expired"
       )`,
      10000
    ));
    await cdp.send("Page.navigate", {
      url: `${baseURL}/#token=${currentToken}`
    });
    await waitFor(cdp, "document.readyState === 'complete'");
    await waitFor(
      cdp,
      `document.getElementById("connectionState").textContent.trim() === "LIVE"`,
      10000
    );
    await waitFor(
      cdp,
      `document.getElementById("timelineBody").innerText.includes(
        ${JSON.stringify(filePath)}
      )`,
      10000
    );
    await capture(cdp, "webui-console.png");

    await cdp.send("Emulation.setDeviceMetricsOverride", {
      width: 390,
      height: 844,
      deviceScaleFactor: 1,
      mobile: true
    });
    await delay(150);
    const responsiveLayout = Boolean(await evalValue(cdp, `(() => {
      const grid = document.querySelector(".row-grid");
      const columns = grid ?
        getComputedStyle(grid).gridTemplateColumns.trim().split(/\\s+/) : [];
      return document.documentElement.scrollWidth <=
          document.documentElement.clientWidth + 1 &&
        columns.length === 1;
    })()`));
    if (!responsiveLayout) {
      throw new Error("mobile console has horizontal overflow or dense rows");
    }
    await capture(cdp, "webui-mobile.png");
    await cdp.send("Emulation.clearDeviceMetricsOverride");

    const domMetrics = await evalValue(cdp, `(() => {
      const bodies = Array.from(document.querySelectorAll(".history-body"));
      const mounted = bodies.map((body) => {
        const stack = body.querySelector(":scope > .stack");
        return stack ? stack.children.length :
          body.querySelectorAll(":scope > *").length;
      });
      return {
        nodeCount:document.querySelectorAll("*").length,
        maxMountedRows:Math.max(...mounted)
      };
    })()`);
    const chromeMetrics = await cdp.send("Performance.getMetrics");
    const performanceEvidence = {
      loadToLiveMs: loadToLiveMS,
      maxFilterMs: maxFilterMS,
      liveUpdateMs: liveUpdateMS,
      configurationMs: configurationMS,
      domNodeCount: domMetrics.nodeCount,
      maxMountedRows: domMetrics.maxMountedRows,
      chromeMetrics: Object.fromEntries(
        (chromeMetrics.metrics || []).map((entry) => [
          entry.name,
          entry.value
        ])
      ),
      thresholds: {
        loadToLiveMs: 5000,
        maxFilterMs: 2000,
        liveUpdateMs: 3000,
        configurationMs: 5000,
        domNodeCount: 15000,
        maxMountedRows: 200
      }
    };
    if (loadToLiveMS > 5000 || maxFilterMS > 2000 ||
        liveUpdateMS > 3000 || configurationMS > 5000 ||
        domMetrics.nodeCount > 15000 ||
        domMetrics.maxMountedRows > 200) {
      throw new Error(
        `browser performance threshold failed: ` +
        `${JSON.stringify(performanceEvidence)}`
      );
    }
    await secureWrite(
      "performance.json",
      JSON.stringify(redact(performanceEvidence), null, 2) + "\n",
      "utf8"
    );

    const domText = await evalValue(cdp, "document.body.innerText");
    await secureWrite(
      "dom-summary.txt",
      redact(domText) + "\n",
      "utf8"
    );
    const networkSummary = redact(cdp.events
      .filter((event) => event.method === "Network.requestWillBeSent")
      .map((event) => ({
        url: event.params.request.url,
        method: event.params.request.method
      })));
    await secureWrite(
      "network-summary.json",
      JSON.stringify(networkSummary, null, 2) + "\n",
      "utf8"
    );

    const result = {
      panelsVisible,
      requiredAreasVisible,
      decisionReviewVisible,
      staleDecisionSuppressed,
      liveUpdateObserved,
      hiddenPollingDetected,
      activity: {
        sessionId: sessionID,
        recordCount,
        factsMatched: Boolean(networkFacts),
        executionTree: Boolean(executionFacts.tree),
        guestIdentity: Boolean(executionFacts.identity),
        correlation: Boolean(correlation && focusReturned),
        boundedDOM,
        filtersExercised
      },
      actionRoundTrip,
      noticeAcknowledgement,
      authFailureObserved,
      credentialRefreshObserved,
      staleReadOnlyObserved,
      keyboardNavigation,
      responsiveLayout,
      accessibilityViolations,
      domNodeCount: domMetrics.nodeCount,
      maxMountedRows: domMetrics.maxMountedRows,
      performance: {
        loadToLiveMs: loadToLiveMS,
        maxFilterMs: maxFilterMS,
        liveUpdateMs: liveUpdateMS,
        configurationMs: configurationMS
      },
      artifacts: {
        "overview-screenshot": "webui-overview.png",
        screenshot: "webui-console.png",
        "stale-screenshot": "webui-stale.png",
        "mobile-screenshot": "webui-mobile.png",
        accessibility: "accessibility.json",
        performance: "performance.json",
        "event-summary": "network-summary.json",
        log: "dom-summary.txt"
      }
    };
    await secureWrite(
      "browser-result.json",
      JSON.stringify(redact(result), null, 2) + "\n",
      "utf8"
    );
  } finally {
    await stopChrome(chrome);
    await rm(profileDir, {recursive: true, force: true});
  }

  if (chromeError.includes("DevToolsActivePort file doesn't exist")) {
    throw new Error(chromeError);
  }
}

main().catch(async (error) => {
  try {
    await secureWrite(
      "browser-error.log",
      redact(error && error.stack || String(error)) + "\n",
      "utf8"
    );
  } catch {
    // The primary failure remains the useful result if evidence writing fails.
  }
  console.error(redact(error && error.stack || error));
  process.exit(1);
});
