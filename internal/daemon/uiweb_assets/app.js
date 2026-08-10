// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole;
  const presentation = root.Presentation;
  const safeText = presentation.safeText;
  const valueLabel = presentation.valueLabel;
  const DOM_ROW_LIMIT = presentation.DOM_ROW_LIMIT;
  const DIALOG_ROW_LIMIT = presentation.DIALOG_ROW_LIMIT;
  const migrationOperationPattern = /^op_[A-Za-z0-9_-]{8,124}$/;
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
  const migrationMode = document.getElementById("migrationMode");
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
    migration: document.getElementById("migrationBody"),
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
  let migrationFlow = null;
  let migrationRequest = 0;
  let migrationRefreshError = "";
  let migrationPollTimer = null;
  let migrationPollLoading = false;
  // A snapshot may finish after a newer SSE request arrives. Monotonic
  // generations keep that newer demand pending instead of clearing it.
  let migrationRefreshRequest = 0;
  let migrationRefreshCompleted = 0;
  let seedRequest = 0;
  let seedLoading = false;
  let reconnectTimer = null;
  let reconnectAttempt = 0;
  let dialogReturnFocus = null;
  let activeDecisionReview = "";
  let attentionMessage = "";
  const decisionClaims = new Map();
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

  /**
   * @param {string} area
   * @param {string} status
   * @param {Array<[string,unknown]>} fields
   * @param {string=} tone
   */
  function overviewCard(area, status, fields, tone) {
    const node = card(area, status, fields, tone);
    node.dataset.overviewArea = area;
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
    migrationMode.textContent = mutable ? "live" : "read-only";
    migrationMode.className = mutable ? "badge live" : "badge stale";
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
    const migrations = root.Migration.operations(snapshot);
    const activeMigrations = migrations.filter((value) =>
      !["complete", "cancelled", "rolled-back", "failed"].includes(value.state)
    ).length;
    document.getElementById("metricWorkloads").textContent =
      String((snapshot.sessions || []).length);
    document.getElementById("metricCoverage").textContent =
      coverage.length ? `${available}/${coverage.length}` : "—";
    document.getElementById("metricRisks").textContent =
      String((details.risks.length ? details.risks : snapshot.risks || []).length);
    document.getElementById("metricOperations").textContent =
      `${activeOperations + activeMigrations}/` +
      `${(snapshot.operations || []).length + migrations.length}`;
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

  /** @param {Object} notice */
  async function acknowledgeNotice(notice) {
    if (!root.State.canMutate(state) || !notice || !notice.id) return;
    try {
      const acknowledgement = await root.Client.noticeAck(notice.id);
      const current = (state.snapshot.notices || []).find(
        (value) => value.id === acknowledgement.noticeId
      );
      if (!current) {
        throw new Error("acknowledged notice is absent from the current snapshot");
      }
      current.acknowledged = true;
      current.status = "acknowledged";
      attentionMessage = `Acknowledged ${notice.id}.`;
      renderAll();
      announce(attentionMessage);
    } catch (error) {
      attentionMessage = `Notice acknowledgement failed: ${String(error)}`;
      renderAll();
      announce(attentionMessage);
    }
  }

  /** @param {Object} notice */
  function noticeCard(notice) {
    const row = card(
      notice.summary || notice.id || "notice",
      notice.acknowledged ? "acknowledged" : "needs acknowledgement",
      [
        ["kind", notice.kind || "notice"],
        ["severity", notice.severity || "info"],
        ["status", notice.status || "unknown"],
        ["profile", notice.profile || "all profiles"],
        ["session", notice.session || "not session-scoped"]
      ],
      notice.severity === "error" ? "severity-high" :
        notice.severity === "warning" ? "severity-medium" : ""
    );
    if (!notice.acknowledged) {
      const actions = element("div", "row-actions");
      const acknowledge = element("button");
      acknowledge.type = "button";
      acknowledge.className = "primary";
      acknowledge.dataset.action = "ack-notice";
      acknowledge.dataset.noticeId = notice.id;
      acknowledge.dataset.requiresAuthority = "true";
      acknowledge.textContent = "Acknowledge";
      acknowledge.disabled = !root.State.canMutate(state);
      acknowledge.addEventListener("click", () => {
        acknowledge.disabled = true;
        acknowledgeNotice(notice);
      });
      actions.append(acknowledge);
      row.append(actions);
    }
    return row;
  }

  /** @param {Object} decision */
  function decisionCard(decision) {
    const row = card(
      decision.summary || decision.id || "decision",
      decision.status || "pending",
      [
        ["kind", decision.kind || "decision"],
        ["default", decision.defaultOutcome || "deny"],
        ["profile", decision.profile || "all profiles"],
        ["session", decision.session || "not session-scoped"],
        ["claim", decision.claimSurface || "unclaimed"],
        ["claim expires", decision.claimExpiresAt || "not claimed"]
      ],
      ["pending", "claimed"].includes(decision.status) ? "seeding" : ""
    );
    const actions = element("div", "row-actions");
    const review = element("button");
    review.type = "button";
    review.dataset.action = "review-decision";
    review.dataset.decisionId = decision.id;
    review.textContent = "Review decision";
    review.addEventListener("click", () => openDecisionReview(decision));
    actions.append(review);
    row.append(actions);
    return row;
  }

  /** @param {Object} decision @param {string=} errorMessage */
  function renderDecisionReview(decision, errorMessage = "") {
    if (!decision || activeDecisionReview !== decision.id) return;
    const owned = decisionClaims.get(decision.id);
    const nodes = [
      card(
        decision.preview && decision.preview.summary || decision.id,
        decision.state || "unknown",
        [
          ["decision", decision.id],
          ["kind", decision.kind],
          ["default outcome", decision.defaultOutcome],
          ["allowed actions", decision.allowedActions || []],
          ["timeout", decision.timeoutAt || "unknown"],
          ["revision", decision.revision],
          ["source", decision.source || {}],
          ["risk", decision.risk || {}],
          ["facts", decision.preview && decision.preview.facts || {}],
          ["diff", decision.preview && decision.preview.diff || "none"]
        ],
        ["pending", "claimed"].includes(decision.state) ? "seeding" : ""
      )
    ];
    if (errorMessage) {
      nodes.push(card("Decision action failed", "not applied", [
        ["reason", errorMessage],
        ["recovery", "Refresh the decision and retry only after reviewing its current revision."]
      ], "stale"));
    }
    const actions = element("div", "dialog-actions");
    const close = element("button");
    close.type = "button";
    close.textContent = "Close";
    close.addEventListener("click", closeConsoleDialog);
    actions.append(close);

    if (decision.state === "pending" && !owned) {
      const claim = element("button");
      claim.type = "button";
      claim.className = "primary";
      claim.dataset.requiresAuthority = "true";
      claim.dataset.action = "claim-decision";
      claim.textContent = "Claim for 60 seconds";
      claim.disabled = !root.State.canMutate(state);
      claim.addEventListener("click", async () => {
        claim.disabled = true;
        try {
          const result = await root.Client.decisionClaim(
            decision.id,
            decision.revision
          );
          if (activeDecisionReview !== decision.id) {
            root.Client.decisionRelease(
              decision.id,
              result.claimToken,
              result.revision,
              true
            ).catch(() => {});
            return;
          }
          decisionClaims.set(decision.id, {
            claimToken: result.claimToken,
            revision: result.revision
          });
          renderDecisionReview(await root.Client.decisionInspect(decision.id));
        } catch (error) {
          renderDecisionReview(decision, String(error));
        }
      });
      actions.append(claim);
    } else if (decision.state === "claimed" && owned) {
      const release = element("button");
      release.type = "button";
      release.dataset.requiresAuthority = "true";
      release.textContent = "Release claim";
      release.disabled = !root.State.canMutate(state);
      release.addEventListener("click", async () => {
        release.disabled = true;
        try {
          await root.Client.decisionRelease(
            decision.id,
            owned.claimToken,
            owned.revision
          );
          decisionClaims.delete(decision.id);
          renderDecisionReview(await root.Client.decisionInspect(decision.id));
        } catch (error) {
          renderDecisionReview(decision, String(error));
        }
      });
      actions.append(release);

      const confirmLabel = element("label", "confirm-check");
      const confirm = element("input");
      confirm.type = "checkbox";
      const confirmation = text(
        "I reviewed the redacted preview, risk, default outcome, and exact revision."
      );
      confirmLabel.append(confirm, confirmation);
      nodes.push(confirmLabel);

      const allowed = new Set(decision.allowedActions || []);
      const addResolution = (action, label, className) => {
        const button = element("button");
        button.type = "button";
        button.className = className;
        button.dataset.requiresAuthority = "true";
        button.dataset.action = `${action}-decision`;
        button.textContent = label;
        button.disabled = true;
        confirm.addEventListener("change", () => {
          button.disabled = !confirm.checked || !root.State.canMutate(state);
        });
        button.addEventListener("click", async () => {
          button.disabled = true;
          try {
            const result = await root.Client.decisionResolve(
              decision.id,
              action,
              owned.claimToken
            );
            decisionClaims.delete(decision.id);
            activeDecisionReview = "";
            closeConsoleDialog();
            attentionMessage = `${decision.id} resolved as ${result.status}.`;
            await seedLiveConsole({reason: "decision resolved"});
            announce(attentionMessage);
          } catch (error) {
            renderDecisionReview(decision, String(error));
          }
        });
        actions.append(button);
      };
      if (allowed.has("approve") || allowed.has("apply")) {
        addResolution(
          "approve",
          allowed.has("apply") ? "Apply reviewed decision" : "Approve decision",
          "primary"
        );
      }
      if (allowed.has("deny") || allowed.has("discard")) {
        addResolution(
          "deny",
          allowed.has("discard") ? "Discard decision" : "Deny decision",
          "danger-action"
        );
      }
    }
    nodes.push(actions);
    showConsoleDialog(`Decision · ${decision.id}`, nodes);
  }

  /** @param {Object} decision */
  async function openDecisionReview(decision) {
    if (!decision || !decision.id) return;
    activeDecisionReview = decision.id;
    showConsoleDialog("Decision review", [
      card("Loading current decision", "read-only", [["decision", decision.id]])
    ]);
    try {
      const current = await root.Client.decisionInspect(decision.id);
      renderDecisionReview(current);
    } catch (error) {
      if (activeDecisionReview !== decision.id) return;
      activeDecisionReview = "";
      showConsoleDialog("Decision review unavailable", [
        card("Decision could not be loaded", "not changed", [
          ["decision", decision.id],
          ["reason", String(error)]
        ], "stale")
      ]);
    }
  }

  function renderOverview() {
    const snapshot = state.snapshot;
    const rows = [];
    const actionableDecisions = (snapshot.decisions || []).filter(
      (value) => ["pending", "claimed"].includes(value.status)
    );
    const unacknowledgedNotices = (snapshot.notices || []).filter(
      (value) => !value.acknowledged
    );
    const hostFSWrites = actionableDecisions.filter(
      (value) => value.kind === "hostfs.write"
    );
    const backgroundNotices = unacknowledgedNotices.filter(
      (value) => value.kind === "background.status"
    );
    const activeOperations = (snapshot.operations || []).filter(
      (value) => ![
        "succeeded", "failed", "cancelled", "rolled-back",
        "rollback-unproved", "recovery-required"
      ].includes(value.phase)
    );
    const actionSummary = overviewCard(
      "Action Required",
      actionableDecisions.length || unacknowledgedNotices.length ?
        String(actionableDecisions.length + unacknowledgedNotices.length) :
        "none",
      [
        ["Decisions", actionableDecisions.length],
        ["Notices", unacknowledgedNotices.length],
        ["authority", root.State.canMutate(state) ?
          "fresh authenticated snapshot" : "read-only until reseeded"]
      ],
      actionableDecisions.length || unacknowledgedNotices.length ?
        "severity-medium" : ""
    );
    actionSummary.classList.add("overview-priority");
    rows.push(actionSummary);
    const streamSummary = overviewCard("Stream", state.health.state, [
      ["reason", state.health.reason || "authenticated event stream is current"],
      ["sequence", state.lastSeq]
    ], root.State.canMutate(state) ? "live" : "stale");
    streamSummary.classList.add("overview-signal");
    rows.push(streamSummary);
    rows.push(overviewCard("Decisions", String(actionableDecisions.length), [
      ["pending or claimed", actionableDecisions.length],
      ["review", actionableDecisions.length ?
        "Use Review decision below" : "No pending decisions"]
    ]));
    rows.push(overviewCard("Notices", String(unacknowledgedNotices.length), [
      ["unacknowledged", unacknowledgedNotices.length],
      ["review", unacknowledgedNotices.length ?
        "Use Acknowledge below" : "No unacknowledged notices"]
    ]));
    rows.push(overviewCard("HostFS Writes", String(hostFSWrites.length), [
      ["pending decisions", hostFSWrites.length],
      ["authority", "resolves through the decision center"]
    ]));
    rows.push(overviewCard(
      "Background",
      String(activeOperations.length + backgroundNotices.length),
      [
        ["active operations", activeOperations.length],
        ["status notices", backgroundNotices.length]
      ]
    ));
    rows.push(overviewCard("Doctor", "not run on load", [
      ["check", "hideout doctor --level light"],
      ["repair", "never automatic from this console"]
    ]));
    rows.push(overviewCard("Package/Support", "read-only guidance", [
      ["package", "hideout package verify <install-prefix>"],
      ["support", "hideout support matrix"]
    ]));
    rows.push(overviewCard("Audit", "visible retained state", [
      ["retained activity", (snapshot.activity || []).length],
      ["history", "Use Timeline and Operations for exact records"]
    ]));
    if (attentionMessage) {
      rows.push(card("Latest operator action", "local result", [
        ["message", attentionMessage]
      ], attentionMessage.includes("failed") ? "stale" : "live"));
    }
    rows.push(...unacknowledgedNotices.slice(0, 4).map(noticeCard));
    rows.push(...actionableDecisions.slice(0, 4).map(decisionCard));
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

  function clearMigrationFlow() {
    migrationRequest++;
    if (migrationFlow) {
      migrationFlow.secretInputHandle = "";
      migrationFlow.inspectHandle = "";
    }
    migrationFlow = null;
    closeConsoleDialog();
  }

  /** @param {Object} operation */
  function acceptMigrationOperation(operation) {
    if (!root.Migration.validOperation(operation)) return;
    const values = state.snapshot.migrations || [];
    const index = values.findIndex(
      (value) => value.operationId === operation.operationId
    );
    if (index >= 0) {
      if (values[index].revision <= operation.revision) values[index] = operation;
    } else {
      values.unshift(operation);
    }
    scheduleMigrationPoll();
  }

  /** @param {Object} view */
  function migrationProgressCard(view) {
    return card(
      `${String(view.kind).toUpperCase()} · ${view.phase}`,
      view.state,
      [
        ["operation / revision", `${view.id} / ${view.revision}`],
        ["bundle", view.bundle],
        ["current item", view.currentItem],
        ["logical bytes", view.logical],
        ["encoded bytes", view.encoded],
        ["components", view.components],
        ["elapsed / ETA", `${view.elapsed} / ${view.eta}`],
        ["retained partial bytes", view.retained],
        ["blockers", view.blockers],
        ["next action", view.next]
      ],
      view.recovery.required || view.state === "failed" ? "stale" :
        view.terminal ? "live" : "seeding"
    );
  }

  /** @param {Object} operation */
  function openMigrationOperation(operation) {
    let view;
    try {
      view = root.Migration.operationView(operation);
    } catch {
      return;
    }
    const effects = view.effects.map((effect) => card(
      effect.kind || "migration effect",
      effect.status || "unknown",
      [["summary", effect.summary || "No effect summary reported."]],
      effect.status === "failed" || effect.status === "unproved" ? "stale" : ""
    ));
    const receipt = view.receipt ? card(
      view.receipt.resultCode || "terminal receipt",
      view.receipt.terminalState || view.state,
      [
        ["operation", view.receipt.operationId],
        ["bundle", view.receipt.bundleId],
        ["components", `${view.receipt.completedComponents} / ${view.receipt.totalComponents}`],
        ["claims released", view.receipt.claimsReleased],
        ["completed", view.receipt.completedAt]
      ],
      "live"
    ) : card(
      "Operation is not terminal",
      "in progress",
      [["next action", view.next]]
    );
    const actions = element("div", "dialog-actions");
    const close = element("button");
    close.type = "button";
    close.textContent = "Close";
    close.addEventListener("click", closeConsoleDialog);
    actions.append(close);
    if (root.State.canMutate(state) && view.recovery.required &&
        view.recovery.allowedActions.length === 1) {
      const recover = element("button");
      recover.type = "button";
      recover.className = "primary";
      recover.dataset.requiresAuthority = "true";
      recover.textContent = view.recovery.allowedActions[0] === "resume" ?
        "Resume this operation" : "Review exact recovery";
      recover.addEventListener("click", () =>
        openMigrationRecovery(operation)
      );
      actions.append(recover);
    }
    if (root.State.canMutate(state) && !view.terminal &&
        !view.recovery.required) {
      const cancel = element("button");
      cancel.type = "button";
      cancel.className = "danger-action";
      cancel.dataset.requiresAuthority = "true";
      cancel.textContent = "Review cancellation";
      cancel.addEventListener("click", () =>
        openMigrationCancellation(operation)
      );
      actions.append(cancel);
    }
    showConsoleDialog(
      `Migration · ${view.id}`,
      [
        migrationProgressCard(view),
        group("Effects", effects),
        group("Terminal result", [receipt]),
        actions
      ]
    );
  }

  /** @param {Object} operation */
  function openMigrationCancellation(operation) {
    const view = root.Migration.operationView(operation);
    const notice = element("div", "notice");
    notice.append(text(
      "Cancellation is bound to the displayed operation revision and stops " +
      "only at a safe boundary. Enter alone never cancels."
    ));
    let retain = null;
    const nodes = [notice];
    if (operation.kind === "export") {
      const choice = element("label", "confirm-check");
      retain = element("input");
      retain.type = "checkbox";
      retain.checked = true;
      choice.append(
        retain,
        text("Retain unsealed partial output for inspection (unusable until resumed).")
      );
      nodes.push(choice);
    }
    const confirmLabel = element("label", "confirm-check");
    const confirm = element("input");
    confirm.type = "checkbox";
    confirmLabel.append(
      confirm,
      text(`Cancel ${operation.operationId} at revision ${operation.revision}.`)
    );
    const error = element("p", "form-error");
    error.setAttribute("role", "alert");
    const actions = element("div", "dialog-actions");
    const back = element("button");
    back.type = "button";
    back.textContent = "Back";
    back.addEventListener("click", () => openMigrationOperation(operation));
    const apply = element("button");
    apply.type = "button";
    apply.className = "danger-action";
    apply.dataset.requiresAuthority = "true";
    apply.textContent = "Request cancellation";
    apply.disabled = true;
    confirm.addEventListener("change", () => {
      apply.disabled = !confirm.checked || !root.State.canMutate(state);
    });
    apply.addEventListener("click", async () => {
      apply.disabled = true;
      try {
        const payload = {revision: operation.revision};
        if (retain) payload.retainPartial = retain.checked;
        const updated = await root.Client.migrationAction(
          operation.operationId, "cancel", payload
        );
        acceptMigrationOperation(updated);
        closeConsoleDialog();
        renderAll();
        openMigrationOperation(updated);
      } catch {
        error.textContent =
          "Hideout did not accept cancellation. Refresh the exact operation revision.";
        apply.disabled = false;
      }
    });
    actions.append(back, apply);
    nodes.push(confirmLabel, error, actions);
    showConsoleDialog("Confirm migration cancellation", nodes);
  }

  /** @param {Object} operation */
  function openMigrationRecovery(operation) {
    const action = operation.recovery.allowedActions[0];
    if (action === "manual") {
      showConsoleDialog("Manual migration recovery", [
        migrationProgressCard(root.Migration.operationView(operation)),
        card("Manager requires manual review", operation.recovery.code, [
          ["next action", operation.recovery.nextAction]
        ], "stale")
      ]);
      return;
    }
    if (action === "resume") {
      const passwordLabel = element("label", "field");
      passwordLabel.append(text("Bundle passphrase"));
      const password = element("input");
      password.type = "password";
      password.autocomplete = "off";
      passwordLabel.append(password, text(
        "The value is exchanged for a one-shot in-memory Manager handle."
      , "muted"));
      const error = element("p", "form-error");
      error.setAttribute("role", "alert");
      const actions = element("div", "dialog-actions");
      const back = element("button");
      back.type = "button";
      back.textContent = "Back";
      back.addEventListener("click", () => {
        password.value = "";
        openMigrationOperation(operation);
      });
      const resume = element("button");
      resume.type = "button";
      resume.className = "primary";
      resume.dataset.requiresAuthority = "true";
      resume.textContent = "Resume exact revision";
      resume.addEventListener("click", async () => {
        const passphrase = password.value;
        password.value = "";
        if (!passphrase || !root.State.canMutate(state)) return;
        resume.disabled = true;
        try {
          const handle = await root.Client.migrationSecretInput({
            purpose: operation.kind === "export" ? "export-resume" : "import",
            operationId: operation.operationId,
            passphrase
          });
          const updated = await root.Client.migrationAction(
            operation.operationId,
            "resume",
            {revision: operation.revision, secretInputHandle: handle.handle}
          );
          acceptMigrationOperation(updated);
          closeConsoleDialog();
          renderAll();
          openMigrationOperation(updated);
        } catch {
          error.textContent =
            "Resume was not accepted. Check the passphrase and refresh current status.";
          resume.disabled = false;
        }
      });
      actions.append(back, resume);
      showConsoleDialog("Resume migration", [
        card(operation.operationId, operation.recovery.code, [
          ["revision", operation.revision],
          ["next action", operation.recovery.nextAction]
        ]),
        passwordLabel,
        error,
        actions
      ]);
      return;
    }
    const confirmLabel = element("label", "confirm-check");
    const confirm = element("input");
    confirm.type = "checkbox";
    confirmLabel.append(
      confirm,
      text(`${operation.recovery.nextAction} This is bound to revision ${operation.revision}.`)
    );
    const error = element("p", "form-error");
    const actions = element("div", "dialog-actions");
    const back = element("button");
    back.type = "button";
    back.textContent = "Back";
    back.addEventListener("click", () => openMigrationOperation(operation));
    const recover = element("button");
    recover.type = "button";
    recover.className = "danger-action";
    recover.dataset.requiresAuthority = "true";
    recover.textContent = `Apply ${action}`;
    recover.disabled = true;
    confirm.addEventListener("change", () => {
      recover.disabled = !confirm.checked || !root.State.canMutate(state);
    });
    recover.addEventListener("click", async () => {
      recover.disabled = true;
      try {
        const updated = await root.Client.migrationAction(
          operation.operationId,
          "recover",
          {revision: operation.revision, action}
        );
        acceptMigrationOperation(updated);
        closeConsoleDialog();
        renderAll();
        openMigrationOperation(updated);
      } catch {
        error.textContent =
          "Recovery was not accepted. Refresh the exact operation revision.";
        recover.disabled = false;
      }
    });
    actions.append(back, recover);
    showConsoleDialog("Confirm exact migration recovery", [
      migrationProgressCard(root.Migration.operationView(operation)),
      confirmLabel,
      error,
      actions
    ]);
  }

  /** @param {string} label @param {HTMLElement} control @param {string=} help */
  function migrationField(label, control, help) {
    const node = element("label", "field");
    node.append(text(label), control);
    if (help) node.append(text(help, "muted"));
    return node;
  }

  function openMigrationExport() {
    if (!root.State.canMutate(state)) return;
    const environments = (state.snapshot.environments || []).slice()
      .sort((left, right) => String(left.name).localeCompare(String(right.name)));
    const scope = element("div", "stack");
    const selections = [];
    for (const environment of environments) {
      const label = element("label", "confirm-check");
      const checkbox = element("input");
      checkbox.type = "checkbox";
      checkbox.dataset.environmentName = environment.name;
      selections.push(checkbox);
      label.append(
        checkbox,
        text(`${environment.name} · ${environment.status} · ${environment.backend || "backend unknown"}`)
      );
      scope.append(label);
    }
    if (!environments.length) {
      scope.append(text("No environment is present in the verified snapshot.", "muted"));
    }
    const mode = element("select");
    for (const [value, label] of [
      ["config", "Configuration only (safe default)"],
      ["full", "Full VM state and persistent disks"]
    ]) {
      const option = element("option");
      option.value = value;
      option.textContent = label;
      mode.append(option);
    }
    const outputPath = element("input");
    outputPath.type = "text";
    outputPath.autocomplete = "off";
    outputPath.placeholder = "/Users/me/Desktop/machine.hideout-migration";
    const secretRefs = element("input");
    secretRefs.type = "text";
    secretRefs.autocomplete = "off";
    secretRefs.placeholder = "optional-secret-ref, another-ref";
    const password = element("input");
    password.type = "password";
    password.autocomplete = "new-password";
    const confirmation = element("input");
    confirmation.type = "password";
    confirmation.autocomplete = "new-password";
    const error = element("p", "form-error");
    error.setAttribute("role", "alert");
    const actions = element("div", "dialog-actions");
    const cancel = element("button");
    cancel.type = "button";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", () => {
      password.value = "";
      confirmation.value = "";
      closeConsoleDialog();
    });
    const review = element("button");
    review.type = "button";
    review.className = "primary";
    review.dataset.requiresAuthority = "true";
    review.textContent = "Build Manager plan";
    review.disabled = !environments.length;
    review.addEventListener("click", async () => {
      let request;
      try {
        request = root.Migration.buildExportRequest({
          mode: mode.value,
          environmentNames: selections.filter((value) => value.checked)
            .map((value) => value.dataset.environmentName),
          includeSecretRefs: secretRefs.value,
          outputPath: outputPath.value
        });
      } catch (cause) {
        error.textContent = safeText(cause);
        return;
      }
      const passphrase = password.value;
      const confirmed = confirmation.value;
      password.value = "";
      confirmation.value = "";
      if (!passphrase || passphrase !== confirmed) {
        error.textContent = "Enter the same non-empty bundle passphrase twice.";
        return;
      }
      review.disabled = true;
      migrationFlow = {
        kind: "export", stage: "planning", request,
        secretInputHandle: "", plan: null, result: null, error: ""
      };
      renderAll();
      const requestID = ++migrationRequest;
      try {
        const handle = await root.Client.migrationSecretInput({
          purpose: "export-create",
          bundlePath: request.outputPath,
          passphrase,
          confirmation: confirmed
        });
        const plan = await root.Client.migrationExportPlan(request);
        if (requestID !== migrationRequest || !migrationFlow ||
            migrationFlow.kind !== "export") return;
        const view = root.Migration.exportPlanView(plan);
        if (plan.mode !== request.mode || plan.outputPath !== request.outputPath) {
          throw new Error("Manager returned a plan for different export choices");
        }
        migrationFlow.stage = "review";
        migrationFlow.plan = plan;
        migrationFlow.secretInputHandle = handle.handle;
        closeConsoleDialog();
        renderAll();
        openMigrationPlanReview(view);
      } catch {
        if (requestID !== migrationRequest || !migrationFlow) return;
        migrationFlow.stage = "error";
        migrationFlow.error =
          "Export planning was not accepted. Check stopped state, path, capabilities, and passphrase.";
        closeConsoleDialog();
        renderAll();
      }
    });
    actions.append(cancel, review);
    showConsoleDialog("Export this computer", [
      card("One encrypted bundle", "safe defaults", [
        ["configuration mode", "profiles and references; no persistent disks"],
        ["full mode", "stopped VM disks; opaque content may include credentials"],
        ["existing destination", "refused; Hideout never overwrites it implicitly"],
        ["passphrase", "memory-only input; not written to URL or browser storage"]
      ]),
      group("Select environments explicitly", [scope]),
      migrationField(
        "Scope",
        mode,
        "Full requires every selected source environment to be stopped."
      ),
      migrationField("Output bundle file", outputPath),
      migrationField(
        "Optional selected secret references",
        secretRefs,
        "Values are included only when named explicitly; the plan shows the risk."
      ),
      migrationField("Create bundle passphrase", password),
      migrationField("Confirm bundle passphrase", confirmation),
      error,
      actions
    ]);
  }

  /** @param {Object} view */
  function openMigrationPlanReview(view) {
    if (!migrationFlow || !migrationFlow.plan) return;
    const plan = migrationFlow.plan;
    const identity = card(
      `${String(view.kind).toUpperCase()} plan · ${view.id}`,
      "reviewed",
      [
        ["digest", view.digest],
        ["confirmation", view.confirmation],
        ["risks", view.risks],
        ["effects", view.effects.map(
          (effect) => `${effect.kind} via ${effect.provider}`
        )]
      ],
      view.blockers && view.blockers.length ? "stale" : "seeding"
    );
    const inventory = view.kind === "export" ? card(
      "Export inventory",
      view.mode,
      [
        ["output", view.outputPath],
        ["included", view.included],
        ["payload estimate", view.payloadEstimate],
        ["environments", view.environmentEstimates.map(
          (value) => `${value.displayName} (${value.environmentRef}) · ${
            root.Migration.bytes(value.estimatedLogicalBytes)
          } · config ${root.Migration.bytes(value.portableConfigLogicalBytes)} · profile state ${
            root.Migration.bytes(value.profileStateLogicalBytes || 0)
          } · disks ${
            value.diskRefs.length ? value.diskRefs.join(", ") : "none"
          }`
        )],
        ["persistent disks", view.diskEstimates.map(
          (value) => `${value.diskRef} · ${value.role} · logical ${
            root.Migration.bytes(value.logicalBytes)
          } · allocated hint ${root.Migration.bytes(value.allocatedBytesHint)} · used by ${
            value.consumers.join(", ")
          }`
        )],
        ["selected secrets", view.secrets],
        ["excluded", view.exclusions]
      ]
    ) : card(
      "Import inventory",
      view.compatibility.available ? "compatible" : "BLOCKED",
      [
        ["backend", view.compatibility.backend],
        ["required / available", `${root.Migration.bytes(view.compatibility.requiredBytes)} / ${root.Migration.bytes(view.compatibility.availableBytes)}`],
        ["objects", view.environments.map(
          (value) => `${value.sourceRef} → ${value.destinationName}`
        )],
        ["identity", view.identities.map(
          (value) => `${value.sourceRef}: ${value.guestPolicy}`
        )],
        ["workspaces", view.workspaces.map(
          (value) => `${value.proposalId}: ${value.decision}`
        )],
        ["secrets", view.secrets.map(
          (value) => `${value.sourceRef}: ${value.decision}`
        )],
        ["authority", [
          ...view.authorities.map((value) => `${value.proposalId}: approved`),
          ...view.disabled.map((value) => `${value}: disabled`)
        ]]
      ],
      view.compatibility.available ? "" : "stale"
    );
    const warnings = (view.warnings || []).map((warning) => card(
      warning.code, "warning", [
        ["summary", warning.summary], ["next", warning.remediation]
      ], "coverage-partial"
    ));
    const blockers = (view.blockers || []).map((blocker) => card(
      blocker.code, "BLOCKER", [
        ["summary", blocker.summary], ["next", blocker.remediation]
      ], "stale"
    ));
    if (view.kind === "import" && !view.compatibility.available) {
      blockers.push(card(
        view.compatibility.reasonCode || "migration.compatibility.unavailable",
        "BLOCKER",
        [["next", "Use a compatible destination or export configuration only."]],
        "stale"
      ));
    }
    const actions = element("div", "dialog-actions");
    const edit = element("button");
    edit.type = "button";
    edit.textContent = "Edit choices";
    edit.addEventListener("click", () => {
      if (view.kind === "export") {
        clearMigrationFlow();
        openMigrationExport();
      } else {
        openMigrationImportDecisions();
      }
    });
    const confirm = element("button");
    confirm.type = "button";
    confirm.className = "primary";
    confirm.dataset.requiresAuthority = "true";
    confirm.textContent = blockers.length ?
      "Apply disabled · resolve blockers" : "Confirm exact plan";
    confirm.disabled = blockers.length > 0 || !root.State.canMutate(state);
    confirm.addEventListener("click", () =>
      openMigrationPlanConfirmation(view)
    );
    actions.append(edit, confirm);
    showConsoleDialog(`Review ${view.kind} plan`, [
      identity,
      inventory,
      group("Warnings", warnings),
      group("Blockers", blockers),
      actions
    ]);
  }

  /** @param {Object} view */
  function openMigrationPlanConfirmation(view) {
    if (!migrationFlow || !migrationFlow.plan) return;
    const phrase = String(view.kind).toUpperCase();
    const checkLabel = element("label", "confirm-check");
    const check = element("input");
    check.type = "checkbox";
    checkLabel.append(
      check,
      text("I reviewed inventory, identity, mappings, risks, effects, and blockers.")
    );
    const typed = element("input");
    typed.type = "text";
    typed.autocomplete = "off";
    const error = element("p", "form-error");
    const actions = element("div", "dialog-actions");
    const back = element("button");
    back.type = "button";
    back.textContent = "Back to review";
    back.addEventListener("click", () => openMigrationPlanReview(view));
    const apply = element("button");
    apply.type = "button";
    apply.className = "danger-action";
    apply.dataset.requiresAuthority = "true";
    apply.textContent = `Apply exact ${view.kind} plan`;
    apply.disabled = true;
    const update = () => {
      apply.disabled = !root.State.canMutate(state) ||
        !check.checked || typed.value !== phrase;
    };
    check.addEventListener("change", update);
    typed.addEventListener("input", update);
    apply.addEventListener("click", () => {
      applyMigrationPlan(view).catch(() => {
        error.textContent =
          "Apply response was not accepted. Refresh Migration before deciding whether to retry.";
      });
    });
    actions.append(back, apply);
    showConsoleDialog(`Confirm ${view.kind}`, [
      card(view.id, "exact reviewed plan", [
        ["digest", view.digest], ["risks", view.risks]
      ]),
      checkLabel,
      migrationField(`Type ${phrase} exactly`, typed),
      error,
      actions
    ]);
  }

  /** @param {Object} view */
  async function applyMigrationPlan(view) {
    if (!migrationFlow || !migrationFlow.plan ||
        !root.State.canMutate(state)) return;
    const flow = migrationFlow;
    const requestID = ++migrationRequest;
    flow.stage = "applying";
    closeConsoleDialog();
    renderAll();
    try {
      const payload = flow.kind === "export" ?
        root.Migration.exportApply(flow.plan, flow.secretInputHandle) :
        root.Migration.importApply(flow.plan, flow.secretInputHandle);
      const result = flow.kind === "export" ?
        await root.Client.migrationExportApply(payload) :
        await root.Client.migrationImportApply(payload);
      if (requestID !== migrationRequest || migrationFlow !== flow) return;
      if (!migrationOperationPattern.test(result.operationId || "") ||
          !result.state || !result.next) {
        throw new Error("invalid migration apply result");
      }
      flow.secretInputHandle = "";
      flow.stage = "terminal";
      flow.result = result;
      renderAll();
      showConsoleDialog("Migration accepted", [
        card(result.operationId, result.state, [
          ["created", result.created], ["next", result.next]
        ], "seeding"),
        text("The durable operation continues even if this browser dialog closes.", "muted")
      ]);
      requestMigrationRefresh();
    } catch {
      if (requestID !== migrationRequest || migrationFlow !== flow) return;
      flow.stage = "error";
      flow.error =
        "Migration apply outcome is unknown or was refused. Refresh status before retrying.";
      renderAll();
    }
  }

  function openMigrationImport() {
    if (!root.State.canMutate(state)) return;
    const bundlePath = element("input");
    bundlePath.type = "text";
    bundlePath.autocomplete = "off";
    bundlePath.placeholder = "/Users/me/Desktop/machine.hideout-migration";
    const password = element("input");
    password.type = "password";
    password.autocomplete = "current-password";
    const error = element("p", "form-error");
    error.setAttribute("role", "alert");
    const actions = element("div", "dialog-actions");
    const cancel = element("button");
    cancel.type = "button";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", () => {
      password.value = "";
      closeConsoleDialog();
    });
    const inspect = element("button");
    inspect.type = "button";
    inspect.className = "primary";
    inspect.dataset.requiresAuthority = "true";
    inspect.textContent = "Unlock and inspect only";
    inspect.addEventListener("click", async () => {
      const path = bundlePath.value.trim();
      const passphrase = password.value;
      password.value = "";
      if (!path.startsWith("/") || !passphrase || !root.State.canMutate(state)) {
        error.textContent = "Enter an absolute bundle path and its passphrase.";
        return;
      }
      inspect.disabled = true;
      migrationFlow = {
        kind: "import", stage: "unlocking", bundlePath: path,
        inspection: null, choices: null, draft: null, plan: null,
        inspectHandle: "", secretInputHandle: "", result: null, error: ""
      };
      renderAll();
      const flow = migrationFlow;
      const requestID = ++migrationRequest;
      try {
        const inspectHandle = await root.Client.migrationSecretInput({
          purpose: "inspect", bundlePath: path, passphrase
        });
        const inspection = await root.Client.migrationImportInspect({
          bundlePath: path,
          secretInputHandle: inspectHandle.handle
        });
        const importHandle = await root.Client.migrationSecretInput({
          purpose: "import", bundlePath: path, passphrase
        });
        if (requestID !== migrationRequest || migrationFlow !== flow) return;
        if (!inspection.binding || !inspection.inventory ||
            inspection.binding.bundleId !== inspection.inventory.bundleId ||
            importHandle.bundleId !== inspection.binding.bundleId) {
          throw new Error("authenticated bundle bindings differ");
        }
        flow.inspection = inspection;
        flow.choices = root.Migration.importChoices(inspection);
        flow.secretInputHandle = importHandle.handle;
        flow.stage = "decisions";
        closeConsoleDialog();
        renderAll();
        openMigrationImportDecisions();
      } catch {
        if (requestID !== migrationRequest || migrationFlow !== flow) return;
        flow.stage = "error";
        flow.error =
          "Bundle authentication or inspection was not accepted. No destination state changed.";
        closeConsoleDialog();
        renderAll();
      }
    });
    actions.append(cancel, inspect);
    showConsoleDialog("Import an encrypted bundle", [
      card("Read-only first step", "no destination changes", [
        ["bundle", "authenticated and inventoried before any selection"],
        ["default selection", "none"],
        ["identity", "Safe Clone"],
        ["workspaces / authority", "disabled"],
        ["secrets", "unresolved"],
        ["conflicts", "rename here, or delete the old VM under a separate plan"]
      ]),
      migrationField("Encrypted bundle file", bundlePath),
      migrationField(
        "Bundle passphrase",
        password,
        "Used only to create one-shot in-memory inspection/import handles."
      ),
      error,
      actions
    ]);
  }

  function openMigrationImportDecisions() {
    if (!migrationFlow || migrationFlow.kind !== "import" ||
        !migrationFlow.inspection || !migrationFlow.choices) return;
    const flow = migrationFlow;
    const inventory = flow.inspection.inventory;
    const environmentNodes = [];
    const environmentControls = new Map();
    for (const choice of flow.choices.environments) {
      const row = element("article", "row migration-choice");
      const selected = element("input");
      selected.type = "checkbox";
      selected.checked = choice.selected;
      selected.dataset.migrationEnvironment = choice.sourceRef;
      const selectLabel = element("label", "confirm-check");
      selectLabel.append(selected, text(
        `${choice.label} · ${choice.sourceRef}`
      ));
      const name = element("input");
      name.type = "text";
      name.autocomplete = "off";
      name.value = choice.destinationName;
      name.dataset.migrationName = choice.sourceRef;
      const policy = element("select");
      policy.dataset.migrationIdentity = choice.sourceRef;
      for (const [value, label] of [
        ["safe-clone", "Safe Clone · rotate guest identity (recommended)"],
        ["exact-guest-restore", "Exact Guest Restore · collision risk"]
      ]) {
        const option = element("option");
        option.value = value;
        option.textContent = label;
        policy.append(option);
      }
      policy.value = choice.policy;
      environmentControls.set(choice.sourceRef, {selected, name, policy});
      row.append(
        selectLabel,
        migrationField("Destination name", name),
        migrationField("Guest identity", policy)
      );
      environmentNodes.push(row);
    }
    const workspaceControls = new Map();
    const workspaceNodes = flow.choices.workspaces.map((choice) => {
      const row = element("article", "row migration-choice");
      const decision = element("select");
      decision.dataset.migrationWorkspaceDecision = choice.proposalId;
      for (const [value, label] of [
        ["disabled", "Disabled (recommended)"], ["mapped", "Map existing host directory"]
      ]) {
        const option = element("option");
        option.value = value;
        option.textContent = label;
        decision.append(option);
      }
      decision.value = choice.decision;
      const destination = element("input");
      destination.type = "text";
      destination.autocomplete = "off";
      destination.placeholder = "/absolute/destination/workspace";
      destination.value = choice.destinationPath;
      destination.dataset.migrationWorkspacePath = choice.proposalId;
      workspaceControls.set(choice.proposalId, {decision, destination});
      row.append(
        text(`${choice.guestPath} · source hint ${choice.hint || "none"}`, "row-title"),
        migrationField("Decision", decision),
        migrationField("Destination path (mapped only)", destination)
      );
      return row;
    });
    const secretControls = new Map();
    const secretNodes = flow.choices.secrets.map((choice) => {
      const row = element("article", "row migration-choice");
      const decision = element("select");
      decision.dataset.migrationSecretDecision = choice.sourceRef;
      const options = [
        ["unresolved", "Unresolved (recommended)"],
        ["existing-ref", "Bind existing destination secret"]
      ];
      if (choice.valueIncluded) {
        options.push(["import-value", "Import encrypted selected value"]);
      }
      for (const [value, label] of options) {
        const option = element("option");
        option.value = value;
        option.textContent = label;
        decision.append(option);
      }
      decision.value = choice.decision;
      const destination = element("input");
      destination.type = "text";
      destination.autocomplete = "off";
      destination.placeholder = "destination-secret-ref";
      destination.value = choice.destinationRef;
      destination.dataset.migrationSecretRef = choice.sourceRef;
      secretControls.set(choice.sourceRef, {decision, destination});
      row.append(
        text(`${choice.label} · value included=${choice.valueIncluded}`, "row-title"),
        migrationField("Decision", decision),
        migrationField("Destination secret reference", destination)
      );
      return row;
    });
    const authorityControls = new Map();
    const authorityNodes = flow.choices.authorities.map((choice) => {
      const row = element("article", "row migration-choice");
      const decision = element("select");
      decision.dataset.migrationAuthorityDecision = choice.proposalId;
      for (const [value, label] of [
        ["disabled", "Disabled (recommended)"],
        ["approved", "Approve reviewed destination JSON"]
      ]) {
        const option = element("option");
        option.value = value;
        option.textContent = label;
        decision.append(option);
      }
      decision.value = choice.decision;
      const destination = element("textarea");
      destination.rows = 3;
      destination.autocomplete = "off";
      destination.placeholder = '{"reviewed":"destination-specific value"}';
      destination.value = choice.destinationValue;
      destination.dataset.migrationAuthorityValue = choice.proposalId;
      authorityControls.set(choice.proposalId, {decision, destination});
      row.append(
        text(`${choice.class} · ${choice.summary}`, "row-title"),
        migrationField("Decision", decision),
        migrationField("Destination authority JSON", destination)
      );
      return row;
    });
    const error = element("p", "form-error");
    error.setAttribute("role", "alert");
    const actions = element("div", "dialog-actions");
    const discard = element("button");
    discard.type = "button";
    discard.textContent = "Discard import session";
    discard.addEventListener("click", () => {
      clearMigrationFlow();
      renderAll();
    });
    const planButton = element("button");
    planButton.type = "button";
    planButton.className = "primary";
    planButton.dataset.requiresAuthority = "true";
    planButton.textContent = "Build Manager plan";
    planButton.addEventListener("click", async () => {
      for (const choice of flow.choices.environments) {
        const controls = environmentControls.get(choice.sourceRef);
        choice.selected = controls.selected.checked;
        choice.destinationName = controls.name.value;
        choice.policy = controls.policy.value;
      }
      for (const choice of flow.choices.workspaces) {
        const controls = workspaceControls.get(choice.proposalId);
        choice.decision = controls.decision.value;
        choice.destinationPath = controls.destination.value;
      }
      for (const choice of flow.choices.secrets) {
        const controls = secretControls.get(choice.sourceRef);
        choice.decision = controls.decision.value;
        choice.destinationRef = controls.destination.value;
      }
      for (const choice of flow.choices.authorities) {
        const controls = authorityControls.get(choice.proposalId);
        choice.decision = controls.decision.value;
        choice.destinationValue = controls.destination.value;
      }
      let draft;
      try {
        draft = root.Migration.buildImportDraft(
          flow.inspection, flow.bundlePath, flow.choices
        );
      } catch (cause) {
        error.textContent = safeText(cause);
        return;
      }
      planButton.disabled = true;
      flow.stage = "planning";
      flow.draft = draft;
      renderAll();
      const requestID = ++migrationRequest;
      try {
        const request = Object.assign({}, draft, {
          secretInputHandle: flow.secretInputHandle
        });
        const plan = await root.Client.migrationImportPlan(request);
        if (requestID !== migrationRequest || migrationFlow !== flow) return;
        if (plan.bundlePath !== draft.bundlePath ||
            !plan.bundleBinding ||
            plan.bundleBinding.bundleId !== draft.bundleBinding.bundleId) {
          throw new Error("Manager returned a plan for a different bundle");
        }
        const view = root.Migration.importPlanView(plan);
        flow.plan = plan;
        flow.stage = "review";
        closeConsoleDialog();
        renderAll();
        openMigrationPlanReview(view);
      } catch {
        if (requestID !== migrationRequest || migrationFlow !== flow) return;
        flow.stage = "error";
        flow.error =
          "Import planning was not accepted. Review names, paths, secrets, authority, conflicts, and compatibility.";
        closeConsoleDialog();
        renderAll();
      }
    });
    actions.append(discard, planButton);
    showConsoleDialog("Choose import destinations", [
      card(`Authenticated bundle ${inventory.bundleId}`, "sealed", [
        ["created", inventory.createdAt],
        ["encoded / logical", `${root.Migration.bytes(inventory.encodedBytes)} / ${root.Migration.bytes(inventory.logicalBytes)}`],
        ["components", inventory.components],
        ["safe defaults", "nothing selected; Safe Clone; workspaces and authority disabled; secrets unresolved"],
        ["conflicts", "rename the destination, or separately delete an old VM before replanning"]
      ]),
      group("Select environments and identity", environmentNodes),
      group("Workspace mappings", workspaceNodes),
      group("Secret rebinding", secretNodes),
      group("Destination authority", authorityNodes),
      error,
      actions
    ]);
  }

  function renderMigration() {
    const values = root.Migration.operations(state.snapshot);
    const progressRefresh = root.Migration.progressRefreshRequired(
      state.snapshot
    );
    const container = element("div", "migration-console");
    const toolbar = element("div", "config-toolbar");
    const status = element("p", "muted");
    status.textContent = safeText(
      migrationRefreshError ?
        `${migrationRefreshError}; last verified migration state is retained.` :
        progressRefresh ?
          "Active migration progress refreshes from the authenticated Manager every two seconds." :
          "Migration refresh is event-triggered while idle; periodic refresh starts only for active work."
    );
    const actions = element("div", "row-actions");
    const exportButton = element("button");
    exportButton.type = "button";
    exportButton.className = "primary";
    exportButton.dataset.requiresAuthority = "true";
    exportButton.textContent = "Export this computer";
    exportButton.disabled = !root.State.canMutate(state) || Boolean(migrationFlow);
    exportButton.addEventListener("click", openMigrationExport);
    const importButton = element("button");
    importButton.type = "button";
    importButton.dataset.requiresAuthority = "true";
    importButton.textContent = "Import bundle";
    importButton.disabled = !root.State.canMutate(state) || Boolean(migrationFlow);
    importButton.addEventListener("click", openMigrationImport);
    actions.append(exportButton, importButton);
    toolbar.append(status, actions);
    container.append(toolbar);

    if (migrationFlow) {
      const flowCard = card(
        `Guided ${migrationFlow.kind}`,
        migrationFlow.stage,
        [
          ["plan", migrationFlow.plan && migrationFlow.plan.planId || "not built"],
          ["operation", migrationFlow.result && migrationFlow.result.operationId || "not started"],
          ["error", migrationFlow.error || "none"],
          ["protected input", migrationFlow.secretInputHandle ? "one-shot handle ready" : "not retained"]
        ],
        migrationFlow.stage === "error" ? "stale" : "seeding"
      );
      const flowActions = element("div", "row-actions");
      if (migrationFlow.stage === "review" && migrationFlow.plan) {
        const review = element("button");
        review.type = "button";
        review.textContent = "Open exact plan review";
        review.addEventListener("click", () => openMigrationPlanReview(
          migrationFlow.kind === "export" ?
            root.Migration.exportPlanView(migrationFlow.plan) :
            root.Migration.importPlanView(migrationFlow.plan)
        ));
        flowActions.append(review);
      }
      if (migrationFlow.kind === "import" &&
          migrationFlow.stage === "decisions") {
        const decisions = element("button");
        decisions.type = "button";
        decisions.textContent = "Continue destination decisions";
        decisions.addEventListener("click", openMigrationImportDecisions);
        flowActions.append(decisions);
      }
      if (!["planning", "applying", "unlocking"].includes(migrationFlow.stage)) {
        const discard = element("button");
        discard.type = "button";
        discard.textContent = "Discard local flow";
        discard.addEventListener("click", () => {
          clearMigrationFlow();
          renderAll();
        });
        flowActions.append(discard);
      }
      flowCard.append(flowActions);
      container.append(flowCard);
    }

    const rows = values.map((operation) => {
      const view = root.Migration.operationView(operation);
      const node = migrationProgressCard(view);
      const inspect = element("button");
      inspect.type = "button";
      inspect.textContent = view.recovery.required ?
        "Inspect and recover" : "Inspect progress";
      inspect.addEventListener("click", () => openMigrationOperation(operation));
      node.append(inspect);
      return node;
    });
    const stack = element("div", "stack");
    if (rows.length) stack.append(...rows.slice(0, DOM_ROW_LIMIT));
    else stack.append(text(
      "No migration has been started on this computer. Export or import opens a guided review.",
      "empty"
    ));
    container.append(group("Durable migration operations", [stack]));
    bodies.migration.replaceChildren(container);
  }

  /** @param {boolean} clearRequest */
  function cancelMigrationPoll(clearRequest) {
    if (migrationPollTimer !== null) {
      window.clearTimeout(migrationPollTimer);
      migrationPollTimer = null;
    }
    if (clearRequest) migrationRefreshCompleted = migrationRefreshRequest;
  }

  function migrationPollRequired() {
    return Boolean(
      state &&
      root.Client.hasCredential() &&
      root.State.canMutate(state) &&
      (migrationRefreshRequest > migrationRefreshCompleted ||
       root.Migration.progressRefreshRequired(state.snapshot))
    );
  }

  function scheduleMigrationPoll() {
    cancelMigrationPoll(false);
    if (!migrationPollRequired()) return;
    migrationPollTimer = window.setTimeout(() => {
      migrationPollTimer = null;
      pollMigrationSnapshot();
    }, 2000);
  }

  function requestMigrationRefresh() {
    migrationRefreshRequest++;
    pollMigrationSnapshot();
  }

  async function pollMigrationSnapshot() {
    if (migrationPollLoading || !migrationPollRequired()) {
      scheduleMigrationPoll();
      return;
    }
    migrationPollLoading = true;
    const refreshRequest = migrationRefreshRequest;
    try {
      const snapshot = await root.Client.snapshot();
      if (snapshot.instanceId !== state.instanceId ||
          snapshot.credentialGeneration !== state.credentialGeneration) {
        seedLiveConsole({reason: "migration snapshot authority changed"}).catch(() => {});
        return;
      }
      state.snapshot.migrations = root.Migration.mergeOperations(
        state.snapshot.migrations || [], snapshot.migrations || []
      );
      migrationRefreshCompleted = Math.max(
        migrationRefreshCompleted,
        refreshRequest
      );
      migrationRefreshError = "";
      renderAll();
    } catch (error) {
      if (!(error && error.credentialExpired)) {
        migrationRefreshError = "Migration progress refresh failed";
        renderAll();
      }
    } finally {
      migrationPollLoading = false;
      scheduleMigrationPoll();
    }
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
    renderMigration();
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
        root.State.streamConnected(state);
        renderAll();
        scheduleMigrationPoll();
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
        if (event.kind === "background" &&
            String(event.payload && event.payload.op || "").startsWith(
              "migration-"
            )) {
          requestMigrationRefresh();
        }
      },
      error: (reason) => {
        if (!state.requiresReseed) root.State.disconnect(state, reason);
        stream = null;
        renderAll();
        scheduleAuthoritativeReseed(reason);
      }
    }, state.lastSeq);
  }

  /**
   * @param {{automatic?:boolean,reason?:string}=} options
   */
  async function seedLiveConsole(options = {}) {
    const automatic = Boolean(options.automatic);
    if (!automatic) reconnectAttempt = 0;
    cancelReconnect(false);
    cancelMigrationPoll(false);
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
      migrationRefreshCompleted = migrationRefreshRequest;
      seedLoading = false;
      syncSessionScope();
      details = emptyDetails();
      renderAll();
      connectEvents();
      loadDetails();
      scheduleMigrationPoll();
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
    const decisionID = activeDecisionReview;
    const owned = decisionClaims.get(decisionID);
    activeDecisionReview = "";
    if (owned) {
      decisionClaims.delete(decisionID);
      root.Client.decisionRelease(
        decisionID,
        owned.claimToken,
        owned.revision,
        true
      ).catch(() => {});
    }
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
  window.addEventListener("pagehide", () => {
    for (const [decisionID, owned] of decisionClaims.entries()) {
      root.Client.decisionRelease(
        decisionID,
        owned.claimToken,
        owned.revision,
        true
      ).catch(() => {});
    }
    decisionClaims.clear();
    activeDecisionReview = "";
  });
  root.Client.onAuthorityLost((authority) => {
    cancelReconnect(true);
    cancelMigrationPoll(true);
    seedRequest++;
    seedLoading = false;
    if (stream) {
      stream.close();
      stream = null;
    }
    decisionClaims.clear();
    activeDecisionReview = "";
    const reason = authority.reason || "operator credential was rejected";
    if (state) root.State.expireCredential(state, reason);
    else state = root.State.unavailable("credential-expired", reason);
    renderAll();
  });
  root.Client.onCredentialRefresh(() => {
    cancelReconnect(true);
    decisionClaims.clear();
    activeDecisionReview = "";
    seedLiveConsole({
      reason: "fresh operator credential received from URL fragment"
    }).catch(() => {});
  });
  renderHelp();
  seedLiveConsole().catch(() => {});
})();
