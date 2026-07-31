// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole;
  const presentation = root.Presentation;
  const safeText = presentation.safeText;
  const valueLabel = presentation.valueLabel;
  const DOM_ROW_LIMIT = presentation.DOM_ROW_LIMIT;
  const DIALOG_ROW_LIMIT = presentation.DIALOG_ROW_LIMIT;
  const cardTones = new Set([
    "live", "seeding", "stale",
    "coverage-available", "coverage-partial", "coverage-unavailable",
    "severity-critical", "severity-high", "severity-medium", "severity-low"
  ]);
  const bootstrap = document.getElementById("hideout-bootstrap");
  const connectionState = document.getElementById("connectionState");
  const connectionReason = document.getElementById("connectionReason");
  const staleBanner = document.getElementById("staleBanner");
  const configMode = document.getElementById("configMode");
  const reseedButton = /** @type {HTMLButtonElement} */ (
    document.getElementById("reseed")
  );
  const helpSearch = /** @type {HTMLInputElement} */ (
    document.getElementById("helpSearch")
  );
  const sessionScope = /** @type {HTMLSelectElement} */ (
    document.getElementById("sessionScope")
  );
  const bodies = {
    overview: document.getElementById("overviewBody"),
    timeline: document.getElementById("timelineBody"),
    executions: document.getElementById("executionsBody"),
    files: document.getElementById("filesBody"),
    network: document.getElementById("networkBody"),
    coverage: document.getElementById("coverageBody"),
    risks: document.getElementById("risksBody"),
    operations: document.getElementById("operationsBody"),
    config: document.getElementById("configBody"),
    help: document.getElementById("helpBody")
  };
  let state = null;
  let stream = null;
  let activePanel = "overview";
  let selectedSession = "";
  let detailRequest = 0;
  let detailLoading = false;
  let detailReloadRequested = false;
  let details = emptyDetails();
  let eventFilters = root.Activity.normalizeFilters({});
  let helpCatalog = {schema: "hideout.operator-help.v1", commands: []};
  let selectedProfile = "";
  let configTransaction = null;
  let configRequest = 0;
  let seedRequest = 0;
  let seedLoading = false;
  let reconnectTimer = null;
  let reconnectAttempt = 0;
  let dialogReturnFocus = null;
  let announcedHealth = "";
  const reconnectDelays = Object.freeze([500, 1000, 2000, 4000, 8000]);

  try {
    helpCatalog = JSON.parse(bootstrap.dataset.helpCatalog || "{}");
  } catch {
    helpCatalog = {schema: "hideout.operator-help.v1", commands: []};
  }

  function emptyDetails() {
    return {
      ownerQuery: null,
      summary: null,
      events: [],
      executions: [],
      coverage: [],
      risks: [],
      queryTruncated: false,
      nextCursor: "",
      error: ""
    };
  }

  /** @param {string} name @param {string=} className */
  function element(name, className) {
    const node = document.createElement(name);
    if (className) node.className = className;
    return node;
  }

  /** @param {unknown} value @param {string=} className */
  function text(value, className) {
    const node = element("span", className);
    node.textContent = safeText(value);
    return node;
  }

  /** @param {HTMLElement} target @param {string} message */
  function empty(target, message) {
    target.replaceChildren();
    const node = element("div", "empty");
    node.textContent = safeText(message);
    target.append(node);
  }

  /** @param {unknown} message */
  function announce(message) {
    const target = document.getElementById("consoleAnnouncement");
    const rendered = safeText(message);
    if (!target || target.textContent === rendered) return;
    target.textContent = "";
    window.requestAnimationFrame(() => {
      target.textContent = rendered;
    });
  }

  /**
   * @param {string} title
   * @param {string} status
   * @param {Array<[string,unknown]>} fields
   * @param {string=} tone
   */
  function card(title, status, fields, tone) {
    const node = element("article", "row");
    const head = element("div", "row-head");
    const safeTone = cardTones.has(tone || "") ? tone : "";
    const badge = text(status || "—", `badge ${safeTone}`.trim());
    head.append(text(title || "item", "row-title"), badge);
    node.append(head);
    const list = element("dl", "row-grid");
    for (const [key, value] of fields) {
      const term = element("dt");
      term.textContent = safeText(key);
      const detail = element("dd");
      detail.textContent = valueLabel(value);
      list.append(term, detail);
    }
    node.append(list);
    return node;
  }

  /** @param {HTMLElement} target @param {Array<Node>} rows @param {string} message */
  function replaceRows(target, rows, message, omitted = 0) {
    if (!rows.length) {
      empty(target, message);
      return;
    }
    const stack = element("div", "stack");
    const reserve = omitted > 0 || rows.length > DOM_ROW_LIMIT ? 1 : 0;
    const visible = rows.slice(0, DOM_ROW_LIMIT - reserve);
    stack.append(...visible);
    const hidden = omitted + Math.max(0, rows.length - visible.length);
    if (hidden > 0) {
      const notice = element("div", "dom-limit");
      notice.setAttribute("role", "status");
      notice.textContent = safeText(
        `${hidden} additional rows are retained but not mounted. ` +
        "Narrow the filters or load a smaller page."
      );
      stack.append(notice);
    }
    target.replaceChildren(stack);
  }

  function renderHealth() {
    if (!state) return;
    const mutable = root.State.canMutate(state);
    const healthState = safeText(state.health.state, 64);
    const healthTone = [
      "live", "idle-live", "seeding", "stale", "disconnected",
      "credential-expired", "schema-mismatch"
    ].includes(state.health.state) ? state.health.state : "stale";
    const healthReason = state.health.reason ||
      `daemon ${state.instanceId} · sign-in version ${state.credentialGeneration}`;
    connectionState.textContent = healthState.toUpperCase();
    connectionState.className = `badge ${healthTone}`;
    connectionState.setAttribute(
      "aria-label",
      `Console state: ${healthState || "unknown"}`
    );
    connectionReason.textContent = safeText(healthReason);
    staleBanner.hidden = mutable;
    if (!mutable) {
      switch (state.health.state) {
        case "credential-expired":
          staleBanner.textContent =
            "Credential expired. All changes are disabled. Open a freshly " +
            "issued Hideout WebUI link to sign in again and refresh; the " +
            "fragment credential is removed from the address bar immediately.";
          break;
        case "disconnected":
          staleBanner.textContent =
            "Event stream disconnected. All changes are disabled while the " +
            "console makes bounded read-only refresh attempts.";
          break;
        case "seeding":
          staleBanner.textContent =
            "Refreshing verified state. All changes remain disabled " +
            "until the snapshot and authenticated event stream are live.";
          break;
        default:
          staleBanner.textContent =
            "State is out of date and all changes are disabled. Refresh the " +
            "verified state and authenticated event stream to continue.";
      }
    }
    configMode.textContent = mutable ? "live" : "read-only";
    configMode.className = mutable ? "badge live" : "badge stale";
    configMode.setAttribute(
      "aria-label",
      mutable ?
        "Configuration controls are available" :
        "Configuration controls are read-only"
    );
    reseedButton.disabled = seedLoading;
    reseedButton.textContent = seedLoading ?
      "Refreshing…" :
      state.health.state === "credential-expired" ?
        "Retry after reauthentication" :
        "Refresh snapshot";
    if (!mutable) {
      for (const control of document.querySelectorAll(
        '[data-requires-authority="true"]'
      )) {
        control.disabled = true;
        control.setAttribute("aria-disabled", "true");
      }
    }
    document.getElementById("overviewSequence").textContent = `seq ${state.lastSeq}`;
    const healthAnnouncement =
      `${healthState || "unknown"}. ${healthReason}. ` +
      `Configuration ${mutable ? "available" : "read-only"}.`;
    if (healthAnnouncement !== announcedHealth) {
      announcedHealth = healthAnnouncement;
      announce(healthAnnouncement);
    }
  }

  function renderMetrics() {
    const snapshot = state.snapshot;
    const coverage = details.coverage.length ? details.coverage : snapshot.coverage || [];
    const available = coverage.filter(
      (value) => String(value.state || "").toLowerCase() === "available"
    ).length;
    const activeOperations = (snapshot.operations || []).filter(
      (value) => ![
        "succeeded", "failed", "cancelled", "rolled-back",
        "rollback-unproved", "recovery-required"
      ].includes(value.phase)
    ).length;
    document.getElementById("metricWorkloads").textContent =
      String((snapshot.sessions || []).length);
    document.getElementById("metricCoverage").textContent =
      coverage.length ? `${available}/${coverage.length}` : "—";
    document.getElementById("metricRisks").textContent =
      String((details.risks.length ? details.risks : snapshot.risks || []).length);
    document.getElementById("metricOperations").textContent =
      `${activeOperations}/${(snapshot.operations || []).length}`;
  }

  function syncSessionScope() {
    const sessions = state.snapshot.sessions || [];
    if (!sessions.some((value) => value.id === selectedSession)) {
      selectedSession = sessions.length ? sessions[0].id : "";
    }
    const options = [];
    if (!sessions.length) {
      const option = element("option");
      option.value = "";
      option.textContent = "No workload selected";
      options.push(option);
    }
    for (const session of sessions) {
      const option = element("option");
      option.value = session.id;
      option.textContent = safeText(
        `${session.command || session.id} · ` +
        `${session.state || "unknown"} · ${session.id}`
      );
      options.push(option);
    }
    sessionScope.replaceChildren(...options);
    sessionScope.value = selectedSession;
  }

  function renderRetention() {
    const target = document.getElementById("retentionSummary");
    if (!selectedSession) {
      target.textContent = "Select a workload to inspect retained evidence.";
      return;
    }
    const retention = root.Activity.retentionView(
      state.snapshot,
      selectedSession
    );
    if (!retention) {
      target.textContent = "Exact retained owner is unavailable; detailed history cannot be queried.";
      return;
    }
    const flags = [];
    if (retention.pruned) flags.push("pruned");
    if (retention.corrupt) flags.push("corrupt");
    flags.push(...retention.reasons);
    const gap = root.Activity.retainedGapView(
      details.summary,
      details.coverage,
      eventFilters
    );
    target.textContent = safeText(
      `${retention.earliestAt} → ${retention.latestAt} · ` +
      `${retention.usedBytes}/${retention.limitBytes || "unbounded"} bytes` +
      (flags.length ? ` · ${flags.join(", ")}` : "") +
      (gap.partial ? ` · PARTIAL: ${gap.reasons.join(", ")}` : "")
    );
  }

  function selectedSessionProjection() {
    return (state.snapshot.sessions || []).find(
      (value) => value.id === selectedSession
    ) || null;
  }

  function renderOverview() {
    const snapshot = state.snapshot;
    const rows = [];
    const session = selectedSessionProjection();
    if (session) {
      rows.push(card(
        session.command || session.id,
        session.state || "unknown",
        [
          ["session", session.id],
          ["environment", session.environmentId],
          ["profile", session.profile || "default"],
          ["started", session.startedAt || "unknown"]
        ],
        session.state === "running" ? "live" : ""
      ));
    }
    const profileName = session && session.profile ||
      snapshot.profiles[0] && snapshot.profiles[0].profile;
    const profile = (snapshot.profiles || []).find(
      (value) => value.profile === profileName
    );
    if (profile) {
      const desired = profile.desired && profile.desired.network || {};
      const effective = profile.effective || {};
      rows.push(card(
        `${profile.profile} connection`,
        profile.transition && profile.transition.phase ||
          effective.status || "not-observed",
        [
          ["desired", desired],
          ["effective", effective.network || effective.status],
          ["transition", profile.transition || "none"],
          ["revision", profile.revision]
        ],
        profile.transition ? "seeding" : ""
      ));
    }
    const coverage = details.coverage.length ? details.coverage : snapshot.coverage || [];
    for (const interval of coverage.filter(
      (value) => String(value.state || "").toLowerCase() !== "available"
    ).slice(0, 4)) {
      const view = root.Activity.coverageView(interval);
      rows.push(card(
        `${view.subsystem} coverage`,
        view.state,
        [
          ["reason", view.reason],
          ["dropped events", view.dropped],
          ["retention gap", view.retentionGap],
          ["evidence", view.evidence]
        ],
        `coverage-${view.state.toLowerCase()}`
      ));
    }
    const risks = details.risks.length ? details.risks : snapshot.risks || [];
    for (const finding of risks.slice(0, 3)) {
      const view = root.Activity.riskView(finding);
      rows.push(card(
        view.title,
        view.severity,
        [
          ["why", view.explanation],
          ["confidence", view.confidence],
          ["policy", view.policyStatus],
          ["next", view.nextAction]
        ],
        `severity-${view.severity}`
      ));
    }
    replaceRows(
      bodies.overview,
      rows,
      "No active workload, blocker, or retained risk."
    );
  }

  function renderTimeline() {
    if (detailLoading && !details.events.length) {
      empty(bodies.timeline, "Loading exact-owner timeline…");
      return;
    }
    if (details.error) {
      empty(bodies.timeline, details.error);
      return;
    }
    const gap = root.Activity.retainedGapView(
      details.summary,
      details.coverage,
      eventFilters
    );
    const ordered = root.Activity.newestFirst(details.events)
      .map((record) => ({record, view: root.Activity.eventView(record)}));
    const bounded = presentation.bounded(ordered, DOM_ROW_LIMIT - 3);
    const rows = bounded.items.map(({record, view}) => {
        const row = card(
          view.title,
          view.outcome,
          [
            ["time", view.firstAt === view.lastAt ? view.firstAt : `${view.firstAt} → ${view.lastAt}`],
            ["subject", view.detail],
            ["execution", view.executionId],
            ["pid", view.pid || "—"],
            ["count", view.count],
            ["attribution", view.attribution],
            ["coverage", view.coverageId]
          ]
        );
        const inspect = element("button");
        inspect.type = "button";
        inspect.textContent = "Inspect correlated evidence";
        inspect.addEventListener("click", () => openCorrelation(record));
        row.append(inspect);
        return row;
      });
    if (gap.partial) {
      rows.unshift(card(
        "Retained history has a gap",
        "partial",
        [
          ["reasons", gap.reasons],
          ["retained range", gap.from && gap.to ? `${gap.from} → ${gap.to}` : "unavailable"]
        ],
        "coverage-partial"
      ));
    }
    if (details.queryTruncated) {
      rows.unshift(card(
        "History is truncated",
        "partial",
        [["reason", "More retained events exist; use filters or load the next page."],
          ["next cursor", details.nextCursor || "unavailable"]],
        "coverage-partial"
      ));
    }
    replaceRows(
      bodies.timeline,
      rows,
      "No retained event for this workload.",
      bounded.omitted
    );
    const more = /** @type {HTMLButtonElement} */ (
      document.getElementById("loadMoreActivity")
    );
    more.hidden = !details.nextCursor;
    more.disabled = detailLoading;
  }

  /** @param {Object} record */
  function openCorrelation(record) {
    const correlation = root.Activity.correlate(record, details);
    const rows = [
      card(
        correlation.event.title,
        correlation.event.outcome,
        [
          ["subject", correlation.event.detail],
          ["time", `${correlation.event.firstAt} → ${correlation.event.lastAt}`],
          ["execution", correlation.event.executionId],
          ["coverage", correlation.event.coverageId],
          ["attribution", correlation.event.attribution]
        ]
      )
    ];
    if (correlation.execution) {
      rows.push(card(
        correlation.execution.title,
        correlation.execution.outcome,
        [
          ["execution", correlation.execution.id],
          ["pid", correlation.execution.pid],
          ["argv", correlation.execution.argv],
          ["cwd", correlation.execution.cwd],
          ["activity", correlation.execution.counts]
        ]
      ));
    }
    if (correlation.coverage) {
      rows.push(card(
        `${correlation.coverage.subsystem} coverage`,
        correlation.coverage.state,
        [
          ["reason", correlation.coverage.reason],
          ["dropped", correlation.coverage.dropped],
          ["retention gap", correlation.coverage.retentionGap],
          ["evidence", correlation.coverage.evidence]
        ],
        `coverage-${correlation.coverage.state.toLowerCase()}`
      ));
    }
    for (const risk of correlation.risks.slice(0, 20)) {
      rows.push(card(
        risk.title,
        risk.severity,
        [
          ["why", risk.explanation],
          ["confidence", risk.confidence],
          ["policy", risk.policyStatus],
          ["next", risk.nextAction]
        ],
        `severity-${risk.severity}`
      ));
    }
    const stack = element("div", "stack");
    stack.append(...rows);
    showConsoleDialog(
      `Correlated evidence · ${correlation.event.id || correlation.event.kind}`,
      [stack]
    );
  }

  function renderExecutions() {
    if (detailLoading && !details.executions.length) {
      empty(bodies.executions, "Loading execution ancestry…");
      return;
    }
    if (details.error) {
      empty(bodies.executions, details.error);
      return;
    }
    const tree = element("div", "tree");
    const flattened = root.Activity.flattenExecutions(details.executions);
    const bounded = presentation.bounded(flattened, DOM_ROW_LIMIT - 1);
    for (const entry of bounded.items) {
      const view = root.Activity.executionView(entry.node);
      const node = card(
        view.title,
        view.outcome,
        [
          ["execution", view.id],
          ["pid", view.pid],
          ["argv", view.argv],
          ["cwd", view.cwd],
          ["identity", view.identity],
          ["started", view.startedAt],
          ["activity", view.counts],
          ["limitations", view.limitations],
          ["parent", view.parentUnavailable ? "unavailable (retained child)" : view.parent]
        ]
      );
      node.classList.add("tree-node");
      node.dataset.depth = String(Math.min(entry.depth, 4));
      tree.append(node);
    }
    if (!flattened.length) {
      empty(bodies.executions, "No retained execution tree for this workload.");
      return;
    }
    if (bounded.omitted) {
      const notice = element("div", "dom-limit");
      notice.textContent = safeText(
        `${bounded.omitted} additional execution nodes are retained but not mounted.`
      );
      tree.append(notice);
    }
    bodies.executions.replaceChildren(tree);
  }

  /** @param {string[]} kinds @param {HTMLElement} target @param {string} message */
  function renderEventsByKind(kinds, target, message) {
    if (detailLoading && !details.events.length) {
      empty(target, "Loading exact-owner activity…");
      return;
    }
    if (details.error) {
      empty(target, details.error);
      return;
    }
    const matching = root.Activity.newestFirst(
      details.events.filter((record) => kinds.includes(record.kind))
    );
    const bounded = presentation.bounded(matching, DOM_ROW_LIMIT - 1);
    const rows = bounded.items
      .map(root.Activity.eventView)
      .map((view) => card(
        view.title,
        view.outcome,
        [
          ["time", view.lastAt],
          ["subject", view.detail],
          ["execution", view.executionId],
          ["count", view.count],
          ["bytes", view.bytes || "—"],
          ["attribution", view.attribution],
          ["truncation", view.truncation]
        ]
      ));
    replaceRows(target, rows, message, bounded.omitted);
  }

  function renderCoverage() {
    if (detailLoading && !details.coverage.length) {
      empty(bodies.coverage, "Loading coverage intervals…");
      return;
    }
    if (details.error) {
      empty(bodies.coverage, details.error);
      return;
    }
    const bounded = presentation.bounded(
      details.coverage,
      DOM_ROW_LIMIT - 1
    );
    const rows = bounded.items
      .map(root.Activity.coverageView)
      .map((view) => card(
        view.subsystem,
        view.state,
        [
          ["reason", view.reason],
          ["window", `${view.startedAt} → ${view.endedAt}`],
          ["dropped events", view.dropped],
          ["retention gap", view.retentionGap],
          ["evidence", view.evidence]
        ],
        `coverage-${String(view.state || "").toLowerCase()}`
      ));
    replaceRows(
      bodies.coverage,
      rows,
      "Coverage is unavailable for the selected workload.",
      bounded.omitted
    );
  }

  function renderRisks() {
    if (detailLoading && !details.risks.length) {
      empty(bodies.risks, "Loading explainable risks…");
      return;
    }
    if (details.error) {
      empty(bodies.risks, details.error);
      return;
    }
    const bounded = presentation.bounded(
      details.risks,
      DOM_ROW_LIMIT - 1
    );
    const rows = bounded.items
      .map(root.Activity.riskView)
      .map((view) => card(
        view.title,
        view.severity,
        [
          ["rule", view.rule],
          ["why", view.explanation],
          ["confidence", view.confidence],
          ["policy", [view.policyStatus, view.policyDisposition].filter(Boolean).join(" · ")],
          ["observed", `${view.firstAt} → ${view.lastAt}`],
          ["count", view.count],
          ["evidence", view.evidenceRefs],
          ["next", view.nextAction]
        ],
        `severity-${view.severity}`
      ));
    replaceRows(
      bodies.risks,
      rows,
      "No explainable risk finding.",
      bounded.omitted
    );
  }

  function renderOperations() {
    const bounded = presentation.bounded(
      state.snapshot.operations || [],
      DOM_ROW_LIMIT - 1
    );
    const rows = bounded.items
      .map(root.Activity.operationView)
      .map((view) => {
        const effects = view.effects.map(
          (effect) => `${effect.id}:${effect.phase}@${effect.provider}` +
            (effect.evidence.length ? ` [${effect.evidence.join(", ")}]` : "")
        );
        return card(
          view.title,
          view.phase,
          [
            ["operation", view.id],
            ["owner", view.owner],
            ["effects", effects],
            ["result", view.result],
            ["recovery", view.recovery],
            ["updated", view.updatedAt]
          ],
          ["failed", "rollback-unproved", "recovery-required"].includes(view.phase) ?
            "stale" :
            view.phase === "succeeded" ? "live" : "seeding"
        );
      });
    replaceRows(
      bodies.operations,
      rows,
      "No durable operation history.",
      bounded.omitted
    );
  }

  function syncConfigProfile() {
    const profiles = state.snapshot.profiles || [];
    if (!profiles.some((value) => value.profile === selectedProfile)) {
      const session = selectedSessionProjection();
      selectedProfile = session &&
        profiles.some((value) => value.profile === session.profile) ?
        session.profile :
        profiles.length ? profiles[0].profile : "";
    }
    if (configTransaction) {
      configTransaction = root.Config.sync(
        configTransaction,
        state.snapshot,
        root.State.canMutate(state),
        state.health && state.health.reason || ""
      );
    }
  }

  /** @param {string} title @param {Array<Node>} children */
  function group(title, children) {
    const node = element("section", "review-group");
    const heading = element("h3");
    heading.textContent = safeText(title);
    node.append(heading);
    if (children.length) {
      const stack = element("div", "stack");
      const bounded = presentation.bounded(children, DIALOG_ROW_LIMIT);
      stack.append(...bounded.items);
      if (bounded.omitted) {
        const notice = element("div", "dom-limit");
        notice.textContent = safeText(
          `${bounded.omitted} additional review rows were omitted.`
        );
        stack.append(notice);
      }
      node.append(stack);
    } else {
      const message = element("p", "muted");
      message.textContent = "None";
      node.append(message);
    }
    return node;
  }

  /**
   * @param {string} title
   * @param {Node[]} nodes
   */
  function showConsoleDialog(title, nodes) {
    const dialog = /** @type {HTMLDialogElement} */ (
      document.getElementById("consoleDialog")
    );
    const wasOpen = dialog.open;
    if (!wasOpen &&
        document.activeElement instanceof HTMLElement) {
      dialogReturnFocus = document.activeElement;
    }
    const visible = nodes.slice(0, DIALOG_ROW_LIMIT);
    if (nodes.length > visible.length) {
      const notice = element("div", "dom-limit");
      notice.textContent = safeText(
        `${nodes.length - visible.length} additional dialog sections were omitted.`
      );
      visible.push(notice);
    }
    const titleNode = document.getElementById("dialogTitle");
    titleNode.textContent = safeText(title);
    document.getElementById("dialogBody").replaceChildren(...visible);
    if (!wasOpen) dialog.showModal();
    window.requestAnimationFrame(() => titleNode.focus());
  }

  function closeConsoleDialog() {
    const dialog = /** @type {HTMLDialogElement} */ (
      document.getElementById("consoleDialog")
    );
    if (dialog.open) dialog.close();
  }

  /** @param {Object} field */
  function configField(field) {
    const label = element("label", "field");
    const caption = element("span");
    caption.textContent = safeText(field.label);
    let control;
    if (field.type === "select") {
      control = element("select");
      for (const item of field.options || []) {
        const option = element("option");
        option.value = item.value;
        option.textContent = safeText(item.label);
        control.append(option);
      }
      control.value = field.value || "";
    } else if (field.type === "textarea") {
      control = element("textarea");
      control.rows = 4;
      control.value = field.value || "";
    } else {
      control = element("input");
      control.type = field.type || "text";
      control.value = field.value || "";
      if (field.placeholder) control.placeholder = field.placeholder;
      for (const name of ["min", "max", "step"]) {
        if (field[name] !== undefined) {
          control.setAttribute(name, field[name]);
        }
      }
    }
    control.dataset.configField = field.name;
    control.autocomplete = "off";
    label.append(caption, control);
    if (field.help) {
      const help = element("small", "muted");
      help.textContent = safeText(field.help);
      label.append(help);
    }
    return label;
  }

  /** @param {string} kind */
  function openConfigEditor(kind) {
    if (!selectedProfile || !root.State.canMutate(state)) return;
    const current = root.Config.projection(
      state.snapshot,
      selectedProfile
    );
    if (!current) return;
    if (!configTransaction) {
      try {
        configTransaction = root.Config.createTransaction(
          state.snapshot,
          selectedProfile
        );
      } catch (error) {
        empty(bodies.config, `Cannot create local draft: ${String(error)}`);
        return;
      }
    }
    if (configTransaction.draft.profile !== selectedProfile ||
        ![
          root.Config.STAGE_EDITING,
          root.Config.STAGE_REVIEW,
          root.Config.STAGE_ERROR
        ].includes(configTransaction.stage)) {
      return;
    }
    const model = root.Config.formModel(kind, current);
    const intro = element("div", "notice");
    const copy = element("p");
    copy.textContent = safeText(model.description);
    const scope = element("p", "muted");
    scope.textContent = safeText(
      `Scope: ${model.scope}. Editing remains browser-local until review and confirmation.`
    );
    intro.append(copy, scope);
    const form = element("div", "config-form");
    form.append(...model.fields.map(configField));
    const error = element("p", "form-error");
    error.setAttribute("role", "alert");
    const actions = element("div", "dialog-actions");
    const cancel = element("button");
    cancel.type = "button";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", closeConsoleDialog);
    const add = element("button");
    add.type = "button";
    add.className = "primary";
    add.textContent = "Add to local draft";
    add.addEventListener("click", () => {
      const values = {};
      for (const control of form.querySelectorAll("[data-config-field]")) {
        values[control.dataset.configField] = control.value;
      }
      try {
        const change = root.Config.buildChange(kind, values);
        configTransaction = root.Config.editTransaction(
          configTransaction,
          kind,
          change
        );
        closeConsoleDialog();
        renderAll();
      } catch (cause) {
        error.textContent = safeText(cause);
      }
    });
    actions.append(cancel, add);
    showConsoleDialog(
      `Edit local draft · ${model.title}`,
      [intro, form, error, actions]
    );
  }

  async function reviewConfigDraft() {
    if (!configTransaction || !root.State.canMutate(state)) return;
    const requestID = ++configRequest;
    try {
      configTransaction = root.Config.startReview(configTransaction);
    } catch (error) {
      configTransaction.error = String(error);
      renderAll();
      return;
    }
    renderAll();
    const planned = await root.Config.finishReview(
      root.Client,
      configTransaction
    );
    if (requestID !== configRequest) return;
    configTransaction = planned;
    configTransaction = root.Config.sync(
      configTransaction,
      state.snapshot,
      root.State.canMutate(state),
      state.health && state.health.reason || ""
    );
    renderAll();
    if (configTransaction.stage === root.Config.STAGE_REVIEW) {
      openConfigReview();
    }
  }

  function openConfigReview() {
    if (!configTransaction || !configTransaction.plan) return;
    const view = root.Config.planView(configTransaction.plan);
    const identity = card(
      `${view.profile} · revision ${view.baseRevision}`,
      view.expired ? "expired" : "reviewed",
      [
        ["operation", view.operationId],
        ["plan digest", view.planDigest],
        ["expires", view.expiresAt],
        ["canonical changes", view.changes.map(
          (change) => `${change.kind}: ${valueLabel(change.value)}`
        )]
      ],
      view.expired ? "stale" : "seeding"
    );
    const diffs = view.diff.map((entry) => card(
      entry.field,
      entry.scope,
      [
        ["kind", entry.kind],
        ["before", entry.before],
        ["after", entry.after]
      ]
    ));
    const effects = view.effects.map((effect) => card(
      effect.summary,
      effect.live ? "live effect" : effect.kind,
      [
        ["effect", effect.id],
        ["scope", effect.scope],
        ["provider", effect.provider],
        ["proof required", effect.proofRequired]
      ],
      effect.live ? "seeding" : ""
    ));
    const warnings = view.warnings.map((warning) => card(
      warning.code,
      "warning",
      [["summary", warning.summary]],
      "coverage-partial"
    ));
    const blockers = view.blockers.map((blocker) => card(
      blocker.code,
      "BLOCKER",
      [
        ["summary", blocker.summary],
        ["resource", blocker.resource],
        ["owner / phase", [blocker.owner, blocker.phase].filter(Boolean).join(" · ")],
        ["recovery", blocker.recovery]
      ],
      "stale"
    ));
    const rollback = card(
      view.rollback.summary,
      `rollback · ${view.rollback.mode}`,
      [["effects", view.rollback.effects]]
    );
    const current = root.Config.projection(
      state.snapshot,
      configTransaction.draft.profile
    );
    const status = root.Config.confirmability(
      configTransaction,
      current,
      root.State.canMutate(state)
    );
    const actions = element("div", "dialog-actions");
    const edit = element("button");
    edit.type = "button";
    edit.textContent = "Back to draft";
    edit.addEventListener("click", () => {
      configRequest++;
      configTransaction = root.Config.returnToEdit(configTransaction);
      closeConsoleDialog();
      renderAll();
    });
    const apply = element("button");
    apply.type = "button";
    apply.className = "primary";
    apply.dataset.requiresAuthority = "true";
    apply.textContent = view.blockers.length ?
      "Apply disabled · blockers" : "Choose Apply";
    apply.disabled = !status.allowed;
    apply.addEventListener("click", openConfigConfirmation);
    actions.append(edit, apply);
    const nodes = [
      identity,
      group("Canonical diff", diffs),
      group("Planned effects and proof", effects),
      group("Warnings", warnings),
      group("Blockers", blockers),
      group("Rollback expectation", [rollback])
    ];
    if (!status.allowed) {
      nodes.push(card(
        "Apply is disabled",
        "read-only",
        [["reason", status.reasons]],
        "stale"
      ));
    }
    nodes.push(actions);
    showConsoleDialog("Review configuration plan", nodes);
  }

  function openConfigConfirmation() {
    if (!configTransaction || !configTransaction.plan) return;
    const current = root.Config.projection(
      state.snapshot,
      configTransaction.draft.profile
    );
    try {
      configTransaction = root.Config.startConfirmation(
        configTransaction,
        current,
        root.State.canMutate(state)
      );
    } catch (error) {
      configTransaction.error = String(error);
      closeConsoleDialog();
      renderAll();
      return;
    }
    const highRisk = root.Config.highRisk(configTransaction.plan);
    const notice = element("div", "notice");
    const summary = element("p");
    summary.textContent = safeText(
      `Accept operation ${configTransaction.plan.operationId} for profile ` +
      `${configTransaction.plan.profile}. Enter alone never applies it.`
    );
    notice.append(summary);
    const checkboxLabel = element("label", "confirm-check");
    const checkbox = element("input");
    checkbox.type = "checkbox";
    checkbox.id = "configExplicitConfirmation";
    const checkboxText = text(
      "I reviewed the canonical diff, effects, blockers, and rollback."
    );
    checkboxLabel.append(checkbox, checkboxText);
    let phraseInput = null;
    const nodes = [notice, checkboxLabel];
    if (highRisk) {
      const phraseLabel = element("label", "field");
      const caption = element("span");
      caption.textContent = safeText(
        `Type exact profile name “${configTransaction.plan.profile}”`
      );
      phraseInput = element("input");
      phraseInput.type = "text";
      phraseInput.autocomplete = "off";
      phraseLabel.append(caption, phraseInput);
      nodes.push(phraseLabel);
    }
    const error = element("p", "form-error");
    error.setAttribute("role", "alert");
    const actions = element("div", "dialog-actions");
    const back = element("button");
    back.type = "button";
    back.textContent = "Back to review";
    back.addEventListener("click", () => {
      configTransaction.stage = root.Config.STAGE_REVIEW;
      openConfigReview();
    });
    const confirm = element("button");
    confirm.type = "button";
    confirm.className = "danger-action";
    confirm.dataset.requiresAuthority = "true";
    confirm.textContent = "Confirm and apply exact plan";
    confirm.disabled = true;
    const update = () => {
      confirm.disabled = !root.State.canMutate(state) ||
        !checkbox.checked ||
        Boolean(
          phraseInput &&
          phraseInput.value !== configTransaction.plan.profile
        );
    };
    checkbox.addEventListener("change", update);
    if (phraseInput) phraseInput.addEventListener("input", update);
    confirm.addEventListener("click", () => {
      applyConfigTransaction(phraseInput ? phraseInput.value : "")
        .catch((cause) => {
          error.textContent = safeText(cause);
        });
    });
    actions.append(back, confirm);
    nodes.push(error, actions);
    showConsoleDialog(
      highRisk ?
        "Confirm high-risk configuration" :
        "Confirm configuration",
      nodes
    );
  }

  /** @param {string} typedProfile */
  async function applyConfigTransaction(typedProfile) {
    if (!configTransaction ||
        !configTransaction.plan ||
        !root.State.canMutate(state)) {
      return;
    }
    const current = root.Config.projection(
      state.snapshot,
      configTransaction.draft.profile
    );
    configTransaction = root.Config.startApply(
      configTransaction,
      current,
      root.State.canMutate(state),
      typedProfile
    );
    const requestID = ++configRequest;
    const operationID = configTransaction.plan.operationId;
    closeConsoleDialog();
    renderAll();
    const result = await root.Config.finishApply(
      root.Client,
      configTransaction
    );
    if (requestID !== configRequest ||
        !result.plan ||
        result.plan.operationId !== operationID) {
      return;
    }
    configTransaction = result;
    acceptConfigOutcome(result);
    renderAll();
    openConfigTerminal();
  }

  /** @param {Object} transaction */
  function acceptConfigOutcome(transaction) {
    if (transaction.resultProjection &&
        transaction.resultProjection.profile) {
      const profiles = state.snapshot.profiles || [];
      const index = profiles.findIndex(
        (value) =>
          value.profile === transaction.resultProjection.profile
      );
      if (index >= 0) profiles[index] = transaction.resultProjection;
      else profiles.push(transaction.resultProjection);
    }
    if (transaction.operation && transaction.operation.id) {
      const operations = state.snapshot.operations || [];
      const index = operations.findIndex(
        (value) => value.id === transaction.operation.id
      );
      if (index >= 0) operations[index] = transaction.operation;
      else operations.unshift(transaction.operation);
    }
  }

  function openConfigTerminal() {
    if (!configTransaction ||
        configTransaction.stage !== root.Config.STAGE_TERMINAL) {
      return;
    }
    const view = root.Config.terminalView(configTransaction);
    const identity = card(
      view.operationId || "Unknown operation",
      view.phase,
      [
        ["durable terminal proof", view.terminal ? "yes" : "no"],
        ["response lost / current outcome", view.responseLost ? "yes" : "no"],
        ["result", view.result || "not available"],
        ["error", view.error || "none"]
      ],
      view.terminal && view.phase === "succeeded" ? "live" :
        ["failed", "rollback-unproved"].includes(view.phase) ?
          "stale" : "seeding"
    );
    const effects = view.effects.map((effect) => card(
      effect.id,
      effect.phase,
      [
        ["kind", effect.kind],
        ["provider", effect.provider],
        ["evidence", effect.evidence.map(
          (item) =>
            `${item.code}${item.value ? ` · ${item.value}` : ""}` +
            `${item.observedAt ? ` @ ${item.observedAt}` : ""}`
        )]
      ],
      effect.phase === "succeeded" ? "live" :
        effect.phase === "failed" || effect.phase === "unproved" ?
          "stale" : "seeding"
    ));
    const recovery = view.recovery ? card(
      view.recovery.summary || "Recovery guidance",
      view.recovery.code || "recovery",
      [["next action", view.recovery.nextAction || "Inspect Operations."]]
    ) : card(
      "Recovery evidence unavailable",
      "unproved",
      [["next action", "Refresh current state and inspect Operations."]],
      "stale"
    );
    const actions = element("div", "dialog-actions");
    const operations = element("button");
    operations.type = "button";
    operations.textContent = "Open Operations";
    operations.addEventListener("click", () => {
      closeConsoleDialog();
      document.querySelector('[data-panel="operations"]').click();
    });
    const done = element("button");
    done.type = "button";
    done.className = "primary";
    done.textContent = "Done";
    done.addEventListener("click", () => {
      configRequest++;
      configTransaction = null;
      closeConsoleDialog();
      renderAll();
    });
    actions.append(operations, done);
    showConsoleDialog(
      `Configuration outcome · ${view.phase}`,
      [
        identity,
        group("Effect evidence", effects),
        group("Recovery", [recovery]),
        actions
      ]
    );
  }

  function renderConfig() {
    syncConfigProfile();
    const profiles = root.Config.rows(state.snapshot);
    if (!profiles.length || !selectedProfile) {
      empty(
        bodies.config,
        "No verified profile state is available."
      );
      return;
    }
    const selected = profiles.find(
      (value) => value.profile === selectedProfile
    );
    if (!selected) {
      empty(bodies.config, "Selected profile is unavailable.");
      return;
    }
    const container = element("div", "config-console");
    const toolbar = element("div", "config-toolbar");
    const scope = element("label", "field compact");
    const scopeLabel = element("span");
    scopeLabel.textContent = "Profile";
    const select = element("select");
    for (const profile of profiles) {
      const option = element("option");
      option.value = profile.profile;
      option.textContent = safeText(
        `${profile.profile} · revision ${profile.revision}`
      );
      select.append(option);
    }
    select.value = selectedProfile;
    select.disabled = Boolean(configTransaction);
    select.addEventListener("change", () => {
      selectedProfile = select.value;
      renderAll();
    });
    scope.append(scopeLabel, select);
    const authority = element("p", "muted");
    authority.textContent = safeText(root.State.canMutate(state) ?
      "Edits remain local until Hideout review and explicit confirmation." :
      "Read-only until a fresh, contiguous authenticated snapshot is live.");
    toolbar.append(scope, authority);
    container.append(toolbar);

    const layers = element("div", "config-layers");
    layers.append(
      card(
        `Desired · ${selected.profile}`,
        `revision ${selected.revision}`,
        [
          ["network", selected.desired.network],
          ["environment", selected.desired.environment],
          ["host file access", selected.desired.hostfs],
          ["command proxies", selected.desired.commandProxies],
          ["command adapters", selected.desired.commandAdapters],
          ["activity retention", selected.desired.retention]
        ]
      ),
      card(
        `Effective · ${selected.profile}`,
        selected.effective.status,
        [
          ["observed network", selected.effective.network || "not observed"],
          ["current session snapshots", selected.effective.currentSessions],
          ["older immutable snapshots", selected.effective.olderSessions]
        ],
        selected.effective.status === "available" ? "live" : ""
      ),
      card(
        `Transition · ${selected.profile}`,
        selected.transition.phase,
        [
          ["operation", selected.transition.operationId || "none"],
          ["kind", selected.transition.kind || "none"],
          ["blockers", selected.transition.blockers],
          ["started", selected.transition.startedAt || "none"]
        ],
        selected.transition.active ? "seeding" : ""
      )
    );
    container.append(layers);

    if (configTransaction) {
      const changes = configTransaction.draft.changes || [];
      const draftCard = card(
        "Client-local configuration draft",
        configTransaction.stage,
        [
          ["profile", configTransaction.draft.profile],
          ["base revision", configTransaction.draft.baseRevision],
          ["changes", changes.map((change) => change.kind)],
          ["authority", configTransaction.authorityReason || "no server effect yet"],
          ["error", configTransaction.error || "none"]
        ],
        configTransaction.stage === root.Config.STAGE_STALE ||
          configTransaction.stage === root.Config.STAGE_ERROR ?
          "stale" : "seeding"
      );
      const actions = element("div", "row-actions");
      const discard = element("button");
      discard.type = "button";
      discard.textContent = "Discard local draft";
      discard.disabled =
        configTransaction.stage === root.Config.STAGE_APPLYING;
      discard.addEventListener("click", () => {
        configRequest++;
        configTransaction = null;
        closeConsoleDialog();
        renderAll();
      });
      const review = element("button");
      review.type = "button";
      review.className = "primary";
      review.dataset.action = "review-config-draft";
      review.textContent =
        configTransaction.stage === root.Config.STAGE_PLANNING ?
          "Hideout is planning…" :
          configTransaction.stage === root.Config.STAGE_REVIEW ?
            "Open canonical review" :
            "Review draft";
      review.disabled =
        !root.State.canMutate(state) ||
        !changes.length ||
        ![
          root.Config.STAGE_EDITING,
          root.Config.STAGE_REVIEW
        ].includes(configTransaction.stage);
      review.addEventListener("click", () => {
        if (configTransaction.stage === root.Config.STAGE_REVIEW) {
          openConfigReview();
        } else {
          reviewConfigDraft();
        }
      });
      actions.append(discard, review);
      if (configTransaction.stage === root.Config.STAGE_TERMINAL) {
        const outcome = element("button");
        outcome.type = "button";
        outcome.textContent = "Open operation outcome";
        outcome.addEventListener("click", openConfigTerminal);
        actions.append(outcome);
      }
      draftCard.append(actions);
      container.append(draftCard);
    }

    const fields = element("div", "stack");
    for (const field of selected.fields) {
      const node = card(
        field.label,
        field.scope,
        [
          ["desired", field.desired],
          ["effective", field.effective],
          ["transition", field.transition],
          ["setting ID", field.capability],
          ["availability", field.reason || (
            field.editable ? "available" : "read-only"
          )]
        ]
      );
      const edit = element("button");
      edit.type = "button";
      edit.textContent = configTransaction &&
        configTransaction.draft.changes.some(
          (change) => change.kind === field.kind
        ) ? "Edit local draft change" : "Add to local draft";
      edit.disabled =
        !root.State.canMutate(state) ||
        !field.editable ||
        Boolean(
          configTransaction &&
          ![
            root.Config.STAGE_EDITING,
            root.Config.STAGE_REVIEW,
            root.Config.STAGE_ERROR
          ].includes(configTransaction.stage)
        );
      edit.addEventListener("click", () => openConfigEditor(field.kind));
      node.append(edit);
      fields.append(node);
    }
    if (!selected.fields.length) {
      const message = element("div", "empty");
      message.textContent =
        "Hideout offers no editable settings for this profile.";
      fields.append(message);
    }
    container.append(group("Configuration controls", [fields]));
    bodies.config.replaceChildren(container);
  }

  function renderHelp() {
    const query = (helpSearch.value || "").trim().toLowerCase();
    const commands = (helpCatalog.commands || []).filter((command) => {
      if (!query) return true;
      return JSON.stringify(command).toLowerCase().includes(query);
    });
    const rows = commands.slice(0, 100).map((command) => card(
      command.name || command.id,
      command.audience || "operator",
      [
        ["purpose", command.purpose],
        ["syntax", command.syntax],
        ["examples", command.examples],
        ["before", command.prerequisites],
        ["effects", command.effects],
        ["safety", command.safety],
        ["recovery", command.recovery],
        ["next", command.next]
      ]
    ));
    replaceRows(bodies.help, rows, "No matching command.");
  }

  function renderFilterStatus() {
    const active = [];
    for (const [name, value] of Object.entries(eventFilters)) {
      if (Array.isArray(value) ? value.length : Boolean(value)) {
        active.push(`${name}=${valueLabel(value)}`);
      }
    }
    document.getElementById("activityFilterStatus").textContent = safeText(
      active.length ? active.join(" · ") : "No filters"
    );
  }

  function renderAll() {
    if (!state) return;
    for (const target of [
      bodies.timeline,
      bodies.executions,
      bodies.files,
      bodies.network,
      bodies.coverage,
      bodies.risks
    ]) {
      target.setAttribute("aria-busy", String(detailLoading));
    }
    renderHealth();
    syncSessionScope();
    renderRetention();
    renderMetrics();
    renderOverview();
    renderTimeline();
    renderExecutions();
    renderEventsByKind(
      ["file"],
      bodies.files,
      "No retained file metadata operation."
    );
    renderEventsByKind(
      ["connection", "dns"],
      bodies.network,
      "No retained process-attributed network or DNS operation."
    );
    renderCoverage();
    renderRisks();
    renderOperations();
    renderConfig();
    renderHelp();
    renderFilterStatus();
  }

  async function loadDetails() {
    if (!state || !selectedSession) {
      details = emptyDetails();
      renderAll();
      return;
    }
    if (detailLoading) {
      detailReloadRequested = true;
      return;
    }
    const query = root.Activity.ownerQuery(state.snapshot, selectedSession);
    if (!query) {
      details = emptyDetails();
      details.error =
        "This workload is not present in the current verified state. " +
        "Select its exact environment and VM instance, or its disposable run.";
      renderAll();
      return;
    }
    detailLoading = true;
    detailReloadRequested = false;
    const requestID = ++detailRequest;
    const requestedSession = selectedSession;
    const requestedFilters = JSON.stringify(eventFilters);
    details.ownerQuery = query;
    details.error = "";
    renderAll();
    try {
      const [summary, events, executions, coverage, risks] = await Promise.all([
        root.Client.activity(
          "summary",
          root.Activity.summaryQuery(query, eventFilters)
        ),
        root.Client.activity(
          "events",
          root.Activity.eventQuery(query, eventFilters)
        ),
        root.Client.activity("executions", query),
        root.Client.activity(
          "coverage",
          root.Activity.coverageQuery(query, eventFilters)
        ),
        root.Client.activity(
          "risks",
          root.Activity.risksQuery(query, eventFilters)
        )
      ]);
      if (requestID !== detailRequest ||
          requestedSession !== selectedSession ||
          requestedFilters !== JSON.stringify(eventFilters)) {
        detailReloadRequested = true;
        return;
      }
      details = {
        ownerQuery: query,
        summary,
        events: events.records || [],
        executions: executions.roots || [],
        coverage: coverage.intervals || [],
        risks: risks.findings || [],
        queryTruncated: Boolean(events.queryTruncated),
        nextCursor: events.nextCursor || "",
        error: ""
      };
    } catch (error) {
      if (requestID !== detailRequest ||
          requestedSession !== selectedSession ||
          requestedFilters !== JSON.stringify(eventFilters)) {
        detailReloadRequested = true;
        return;
      }
      details = emptyDetails();
      details.ownerQuery = query;
      details.error = `Detailed activity unavailable: ${String(error)}`;
    } finally {
      if (requestID === detailRequest) detailLoading = false;
      renderAll();
      if (detailReloadRequested) {
        detailReloadRequested = false;
        loadDetails();
      }
    }
  }

  async function loadMoreActivity() {
    if (!details.nextCursor || !details.ownerQuery || detailLoading) return;
    const button = /** @type {HTMLButtonElement} */ (
      document.getElementById("loadMoreActivity")
    );
    button.disabled = true;
    try {
      const page = await root.Client.activity(
        "events",
        root.Activity.cursorQuery(details.ownerQuery, details.nextCursor)
      );
      const known = new Set(details.events.map((value) => value.id));
      for (const record of page.records || []) {
        if (!known.has(record.id)) {
          details.events.push(record);
          known.add(record.id);
        }
      }
      details.events = details.events.slice(0, 2000);
      details.nextCursor = page.nextCursor || "";
      details.queryTruncated = Boolean(page.queryTruncated);
      if (Array.isArray(page.coverage) && page.coverage.length) {
        details.coverage = page.coverage;
      }
      details.error = "";
    } catch (error) {
      details.error =
        `Next retained page was rejected: ${String(error)}. ` +
        "Refresh the snapshot and reapply filters; cursors are owner/filter/revision bound.";
      details.nextCursor = "";
    } finally {
      button.disabled = false;
      renderAll();
    }
  }

  function filterFormValues() {
    return {
      kinds: document.getElementById("filterKinds").value,
      path: document.getElementById("filterPath").value,
      domain: document.getElementById("filterDomain").value,
      ip: document.getElementById("filterIP").value,
      operations: document.getElementById("filterOperations").value,
      executions: document.getElementById("filterExecutions").value,
      risks: document.getElementById("filterRisks").value,
      from: document.getElementById("filterFrom").value,
      to: document.getElementById("filterTo").value
    };
  }

  function clearFilterForm() {
    for (const id of [
      "filterKinds", "filterPath", "filterDomain", "filterIP",
      "filterOperations", "filterExecutions", "filterRisks",
      "filterFrom", "filterTo"
    ]) {
      document.getElementById(id).value = "";
    }
  }

  /** @param {boolean} resetAttempts */
  function cancelReconnect(resetAttempts) {
    if (reconnectTimer !== null) {
      window.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (resetAttempts) reconnectAttempt = 0;
  }

  /** @param {string} reason */
  function scheduleAuthoritativeReseed(reason) {
    if (reconnectTimer !== null ||
        !state ||
        state.health.state === "credential-expired" ||
        !root.Client.hasCredential() ||
        reconnectAttempt >= reconnectDelays.length) {
      return;
    }
    const delay = reconnectDelays[reconnectAttempt++];
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null;
      seedLiveConsole({automatic: true, reason}).catch(() => {});
    }, delay);
  }

  function connectEvents() {
    if (stream) stream.close();
    stream = root.Client.events({
      open: () => {
        cancelReconnect(true);
      },
      event: (event) => {
        const result = root.State.applyEvent(state, event);
        renderAll();
        if (result.status === "stale") {
          if (stream) stream.close();
          stream = null;
          if (state.health.state !== "credential-expired") {
            scheduleAuthoritativeReseed(result.reason || "event stream stale");
          }
          return;
        }
        if (["activity", "coverage", "risk"].includes(event.kind)) {
          loadDetails();
        }
      },
      error: (reason) => {
        if (!state.requiresReseed) root.State.disconnect(state, reason);
        stream = null;
        renderAll();
        scheduleAuthoritativeReseed(reason);
      }
    });
  }

  /**
   * @param {{automatic?:boolean,reason?:string}=} options
   */
  async function seedLiveConsole(options = {}) {
    const automatic = Boolean(options.automatic);
    if (!automatic) reconnectAttempt = 0;
    cancelReconnect(false);
    const requestID = ++seedRequest;
    if (stream) {
      stream.close();
      stream = null;
    }
    seedLoading = true;
    if (state) {
      root.State.beginReseed(
        state,
        options.reason || "refreshing verified state"
      );
      renderAll();
    } else {
      connectionState.textContent = "SEEDING";
      connectionState.className = "badge seeding";
      connectionReason.textContent = "Loading verified state…";
      staleBanner.hidden = true;
      reseedButton.disabled = true;
      reseedButton.textContent = "Refreshing…";
    }
    try {
      const snapshot = await root.Client.snapshot();
      if (requestID !== seedRequest) return;
      state = root.State.reseed(state, snapshot);
      seedLoading = false;
      syncSessionScope();
      details = emptyDetails();
      renderAll();
      connectEvents();
      loadDetails();
    } catch (error) {
      if (requestID !== seedRequest) return;
      seedLoading = false;
      const reason = error && error.message ?
        error.message : String(error);
      const credentialExpired = Boolean(
        error && error.credentialExpired
      );
      if (!state) {
        state = root.State.unavailable(
          credentialExpired ? "credential-expired" : "disconnected",
          reason
        );
      } else if (credentialExpired) {
        root.State.expireCredential(state, reason);
      } else {
        root.State.disconnect(state, reason);
      }
      details = emptyDetails();
      renderAll();
      if (!credentialExpired) scheduleAuthoritativeReseed(reason);
    }
  }

  const tabs = Array.from(document.querySelectorAll("[role=\"tab\"]"));

  /** @param {Element} tab @param {boolean=} moveFocus */
  function activatePanel(tab, moveFocus = false) {
    activePanel = tab.getAttribute("data-panel") || "overview";
    for (const candidate of tabs) {
      const active = candidate === tab;
      candidate.classList.toggle("active", active);
      candidate.setAttribute("aria-selected", String(active));
      candidate.setAttribute("tabindex", active ? "0" : "-1");
    }
    for (const view of document.querySelectorAll("[data-view]")) {
      const active = view.getAttribute("data-view") === activePanel;
      view.hidden = !active;
      view.classList.toggle("active", active);
    }
    tab.scrollIntoView({block: "nearest", inline: "nearest"});
    if (moveFocus && tab instanceof HTMLElement) tab.focus();
    announce(`${safeText(tab.textContent || activePanel)} view selected.`);
  }

  for (const tab of tabs) {
    tab.addEventListener("click", () => activatePanel(tab));
    tab.addEventListener("keydown", (event) => {
      const current = tabs.indexOf(tab);
      let next = current;
      switch (event.key) {
        case "ArrowRight":
          next = (current + 1) % tabs.length;
          break;
        case "ArrowLeft":
          next = (current - 1 + tabs.length) % tabs.length;
          break;
        case "Home":
          next = 0;
          break;
        case "End":
          next = tabs.length - 1;
          break;
        default:
          return;
      }
      event.preventDefault();
      activatePanel(tabs[next], true);
    });
  }
  const consoleDialog = /** @type {HTMLDialogElement} */ (
    document.getElementById("consoleDialog")
  );
  document.getElementById("dialogForm").addEventListener(
    "submit",
    (event) => event.preventDefault()
  );
  document.getElementById("dialogClose").addEventListener(
    "click",
    closeConsoleDialog
  );
  consoleDialog.addEventListener("close", () => {
    if (dialogReturnFocus instanceof HTMLElement &&
        dialogReturnFocus.isConnected) {
      dialogReturnFocus.focus();
    }
    dialogReturnFocus = null;
  });
  reseedButton.addEventListener("click", () => {
    seedLiveConsole({reason: "manual verified state refresh"})
      .catch(() => {});
  });
  document.getElementById("loadMoreActivity").addEventListener(
    "click",
    loadMoreActivity
  );
  document.getElementById("activityFilters").addEventListener(
    "submit",
    (event) => {
      event.preventDefault();
      try {
        eventFilters = root.Activity.normalizeFilters(filterFormValues());
        details = emptyDetails();
        detailReloadRequested = true;
        renderAll();
        loadDetails();
      } catch (error) {
        details.error = `Invalid activity filter: ${String(error)}`;
        renderAll();
      }
    }
  );
  document.getElementById("clearActivityFilters").addEventListener(
    "click",
    () => {
      clearFilterForm();
      eventFilters = root.Activity.normalizeFilters({});
      details = emptyDetails();
      detailReloadRequested = true;
      renderAll();
      loadDetails();
    }
  );
  helpSearch.addEventListener("input", renderHelp);
  sessionScope.addEventListener("change", () => {
    selectedSession = sessionScope.value;
    details = emptyDetails();
    detailReloadRequested = true;
    renderAll();
    loadDetails();
  });
  root.Client.onAuthorityLost((authority) => {
    cancelReconnect(true);
    seedRequest++;
    seedLoading = false;
    if (stream) {
      stream.close();
      stream = null;
    }
    const reason = authority.reason || "operator credential was rejected";
    if (state) root.State.expireCredential(state, reason);
    else state = root.State.unavailable("credential-expired", reason);
    renderAll();
  });
  root.Client.onCredentialRefresh(() => {
    cancelReconnect(true);
    seedLiveConsole({
      reason: "fresh operator credential received from URL fragment"
    }).catch(() => {});
  });
  renderHelp();
  seedLiveConsole().catch(() => {});
})();
