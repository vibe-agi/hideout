// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole = window.HideoutConsole || {};
  const EVENT_VERSION = "hideout.daemon-event/v2";
  const SNAPSHOT_SCHEMA = "hideout.operator-snapshot.v1";
  const LIVE_HEALTH = new Set(["live", "idle-live"]);
  const PROJECTION_LIMIT = 256;
  const TAIL_LIMIT = 20;
  const INSTANCE_PATTERN = /^daemon_[A-Za-z0-9_-]{1,124}$/;
  const CODE_PATTERN = /^[a-z][a-z0-9._-]{0,127}$/;
  const OPERATION_ID_PATTERN = /^op_[A-Za-z0-9_-]{8,124}$/;
  const RISK_ID_PATTERN = /^risk_[A-Za-z0-9_-]{8,124}$/;
  const ACTIVITY_REF_PATTERN = /^act_[A-Za-z0-9_-]{8,124}$/;
  const DIGEST_PATTERN = /^sha256:[a-f0-9]{64}$/;
  const EVIDENCE_CODE_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
  const KNOWN_KINDS = new Set([
    "profile", "transition", "operation", "activity", "coverage", "risk",
    "capability", "environment", "session", "workspace-view", "background",
    "audit", "export", "cleanup", "hostfs-write", "decision", "notice",
    "lifecycle", "terminal"
  ]);
  const REQUIRED_FIELDS = Object.freeze({
    environment: ["id"],
    session: ["id"],
    "workspace-view": [
      "attachmentId", "session", "environmentId", "workspaceId",
      "workspaceLabel", "guestWorkspace", "workspaceTransport",
      "workspaceViewState"
    ],
    background: ["id", "op", "status"],
    audit: ["action", "decision"],
    export: ["status"],
    cleanup: ["status"],
    "hostfs-write": ["decisionId", "operationId", "status"],
    decision: ["decisionId", "recordKind", "status"],
    notice: ["noticeId", "recordKind", "status"],
    terminal: ["reason"]
  });

  /**
   * Browser state deliberately keeps the authoritative operator snapshot as
   * its public projection. Event-only views are added to the in-memory clone;
   * they are discarded, never merged, by an authoritative re-seed.
   *
   * @typedef {{
   *   schema:string,
   *   instanceId:string,
   *   credentialGeneration:number,
   *   sequence:number,
   *   streamHealth:{state:string,reason?:string},
   *   profiles:Array<Object>,
   *   sessions:Array<Object>,
   *   environments:Array<Object>,
   *   activity:Array<Object>,
   *   activityCursor?:string,
   *   activitySummary?:Object,
   *   coverage:Array<Object>,
   *   risks:Array<Object>,
   *   operations:Array<Object>,
   *   migrations:Array<Object>,
   *   capabilities:Array<Object>,
   *   nextActions:Array<string>,
   *   transitions?:Array<Object>,
   *   auditTail?:Array<Object>,
   *   deniedAuditTail?:Array<Object>,
   *   background?:Array<Object>,
   *   exportOutcomes?:Array<Object>,
   *   cleanupOutcomes?:Array<Object>,
   *   hostfsWrites?:Array<Object>,
   *   decisions?:Array<Object>,
   *   notices?:Array<Object>,
   *   lifecycle?:Array<Object>
   * }} OperatorSnapshot
   */

  /**
   * @typedef {{
   *   version:string,
   *   instanceId:string,
   *   credentialGeneration:number,
   *   kind:string,
   *   optional?:boolean,
   *   phase?:string,
   *   seq:number,
   *   entity?:{kind?:string,id?:string,profile?:string,session?:string},
   *   payload?:Object
   * }} ConsoleEvent
   */

  /**
   * @typedef {{
   *   version:string,
   *   snapshot:OperatorSnapshot,
   *   instanceId:string,
   *   credentialGeneration:number,
   *   lastSeq:number,
   *   profileScope:string,
   *   health:{state:string,reason:string},
   *   streamReadyHealth:string,
   *   readOnly:boolean,
   *   requiresReseed:boolean,
   *   diagnostics:Array<string>
   * }} ConsoleState
   */

  /** @param {unknown} value @returns {value is Record<string, any>} */
  function isObject(value) {
    return Boolean(value) && typeof value === "object" && !Array.isArray(value);
  }

  /** @param {unknown} value @returns {any} */
  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  /** @param {unknown} value @returns {boolean} */
  function validTimestamp(value) {
    return typeof value === "string" &&
      value !== "" &&
      value !== "0001-01-01T00:00:00Z" &&
      Number.isFinite(Date.parse(value));
  }

  /** @param {unknown} value @returns {boolean} */
  function boundedText(value, maximum) {
    return typeof value === "string" &&
      value.length <= maximum &&
      !value.includes("\u0000");
  }

  /** @param {unknown} input @returns {OperatorSnapshot} */
  function validateSnapshot(input) {
    if (!isObject(input)) throw new Error("snapshot is missing");
    const snapshot = /** @type {OperatorSnapshot} */ (input);
    if (snapshot.schema !== SNAPSHOT_SCHEMA ||
        !INSTANCE_PATTERN.test(snapshot.instanceId || "") ||
        !Number.isInteger(snapshot.credentialGeneration) ||
        snapshot.credentialGeneration < 1 ||
        !Number.isInteger(snapshot.sequence) ||
        snapshot.sequence < 0 ||
        !isObject(snapshot.streamHealth) ||
        typeof snapshot.streamHealth.state !== "string") {
      throw new Error("snapshot identity is invalid");
    }
    for (const name of [
      "profiles", "sessions", "environments", "activity", "coverage", "risks",
      "operations", "migrations", "capabilities", "nextActions"
    ]) {
      if (!Array.isArray(snapshot[name])) {
        throw new Error(`snapshot ${name} is invalid`);
      }
    }
    return snapshot;
  }

  /** @param {Record<string, any>} projection */
  function validateProfileProjection(projection) {
    if (!isObject(projection) ||
        projection.schema !== "hideout.profile-projection.v1" ||
        typeof projection.profile !== "string" ||
        projection.profile.length === 0 ||
        projection.profile.length > 128 ||
        !Number.isInteger(projection.revision) ||
        projection.revision < 1 ||
        typeof projection.contentDigest !== "string" ||
        projection.contentDigest.length === 0 ||
        !isObject(projection.desired) ||
        projection.desired.name !== projection.profile ||
        !validTimestamp(projection.updatedAt)) {
      throw new Error("profile state is invalid");
    }
  }

  /** @param {Record<string, any>} projection */
  function validateTransitionProjection(projection) {
    const transition = projection && projection.transition;
    if (!isObject(projection) ||
        typeof projection.profile !== "string" ||
        projection.profile.length === 0 ||
        projection.profile.length > 128 ||
        !isObject(transition) ||
        !OPERATION_ID_PATTERN.test(transition.operationId || "") ||
        !CODE_PATTERN.test(transition.kind || "") ||
        !CODE_PATTERN.test(transition.phase || "") ||
        !validTimestamp(transition.startedAt) ||
        !Array.isArray(transition.blockers || []) ||
        (transition.blockers || []).length > 64 ||
        (transition.blockers || []).some(
          (value) => !CODE_PATTERN.test(value || "")
        )) {
      throw new Error("profile transition state is invalid");
    }
  }

  /** @param {Record<string, any>} operation */
  function validateOperationProjection(operation) {
    const phases = new Set([
      "planned", "claimed", "staging", "activating", "proving",
      "rolling-back", "succeeded", "failed", "cancelled", "rolled-back",
      "rollback-unproved", "recovery-required"
    ]);
    const terminalResults = new Map([
      ["succeeded", "succeeded"],
      ["failed", "failed"],
      ["cancelled", "cancelled"],
      ["rolled-back", "rolled-back"],
      ["rollback-unproved", "unproved"]
    ]);
    const effectKinds = new Set([
      "persist", "stage", "activate", "drain", "restart", "cleanup", "prove"
    ]);
    const effectPhases = new Set([
      "pending", "running", "succeeded", "failed", "rolled-back", "unproved"
    ]);
    const effects = operation && (operation.effects || []);
    const result = operation && operation.result;
    const resultExpected = operation && terminalResults.get(operation.phase);
    if (!isObject(operation) ||
        operation.schema !== "hideout.operation.v1" ||
        !OPERATION_ID_PATTERN.test(operation.id || "") ||
        !CODE_PATTERN.test(operation.kind || "") ||
        !isObject(operation.owner) ||
        !["profile", "environment", "session", "secret"].includes(operation.owner.kind) ||
        !boundedText(operation.owner.id, 128) ||
        operation.owner.id.length === 0 ||
        operation.owner.id.trim() !== operation.owner.id ||
        !DIGEST_PATTERN.test(operation.planDigest || "") ||
        !phases.has(operation.phase) ||
        !Array.isArray(effects) ||
        effects.length > 256 ||
        effects.some((effect) =>
          !isObject(effect) ||
          typeof effect.id !== "string" ||
          effect.id.length === 0 ||
          effect.id.length > 128 ||
          effect.id.trim() !== effect.id ||
          !effectKinds.has(effect.kind) ||
          typeof effect.provider !== "string" ||
          effect.provider.length === 0 ||
          effect.provider.length > 128 ||
          effect.provider.trim() !== effect.provider ||
          !effectPhases.has(effect.phase) ||
          !Array.isArray(effect.evidence || []) ||
          (effect.evidence || []).length > 256 ||
          (effect.evidence || []).some((evidence) =>
            !isObject(evidence) ||
            !EVIDENCE_CODE_PATTERN.test(evidence.code || "") ||
            !boundedText(evidence.value || "", 1024)
          )
        ) ||
        new Set(effects.map((effect) => effect.id)).size !== effects.length ||
        !isObject(operation.recovery) ||
        !EVIDENCE_CODE_PATTERN.test(operation.recovery.code || "") ||
        typeof operation.recovery.summary !== "string" ||
        operation.recovery.summary.length === 0 ||
        operation.recovery.summary.length > 2048 ||
        operation.recovery.summary.includes("\u0000") ||
        !boundedText(operation.recovery.nextAction || "", 1024) ||
        !validTimestamp(operation.createdAt) ||
        !validTimestamp(operation.updatedAt) ||
        Date.parse(operation.updatedAt) < Date.parse(operation.createdAt) ||
        (resultExpected ? (
          !isObject(result) ||
          result.status !== resultExpected ||
          typeof (result.code || "") !== "string" ||
          (result.code || "").length > 128 ||
          typeof (result.summary || "") !== "string" ||
          (result.summary || "").length > 2048
        ) : isObject(result))) {
      throw new Error("operation state is invalid");
    }
  }

  /** @param {Record<string, any>} projection */
  function validateActivityProjection(projection) {
    if (!isObject(projection) ||
        typeof (projection.cursor || "") !== "string" ||
        (projection.cursor || "").length > 4096 ||
        typeof (projection.profile || "") !== "string" ||
        (projection.profile || "").length > 128 ||
        typeof (projection.session || "") !== "string" ||
        (projection.session || "").length > 128 ||
        !Array.isArray(projection.counts || []) ||
        (projection.counts || []).length > 64) {
      throw new Error("activity state is invalid");
    }
    const seen = new Set();
    for (const count of projection.counts || []) {
      if (!isObject(count) ||
          !CODE_PATTERN.test(count.kind || "") ||
          !Number.isInteger(count.count) ||
          count.count < 0 ||
          seen.has(count.kind)) {
        throw new Error("activity state is invalid");
      }
      seen.add(count.kind);
    }
  }

  /** @param {unknown} value */
  function validateCoverageProjection(value) {
    if (!Array.isArray(value) || value.length === 0 || value.length > 64) {
      throw new Error("coverage state is empty or too large");
    }
    for (const interval of value) {
      if (!isObject(interval) ||
          interval.schema !== "hideout.coverage-interval.v1" ||
          !/^cov_[A-Za-z0-9_-]{8,124}$/.test(interval.id || "") ||
          !isObject(interval.owner) ||
          !["reusable-environment", "disposable-session"].includes(
            interval.owner.kind
          ) ||
          typeof interval.owner.backend !== "string" ||
          interval.owner.backend.length === 0 ||
          interval.owner.backend.length > 32 ||
          typeof interval.owner.backendIncarnationId !== "string" ||
          interval.owner.backendIncarnationId.length === 0 ||
          interval.owner.backendIncarnationId.length > 256 ||
          !/^ses_[A-Za-z0-9_-]{1,124}$/.test(interval.sessionId || "") ||
          !["process", "file", "network", "dns"].includes(interval.subsystem) ||
          !["Available", "Partial", "Unavailable"].includes(interval.state) ||
          !CODE_PATTERN.test(interval.reason || "") ||
          !Number.isInteger(interval.collectorGeneration) ||
          interval.collectorGeneration < 1 ||
          !validTimestamp(interval.startedAt) ||
          !Array.isArray(interval.evidence || []) ||
          (interval.evidence || []).length > 256) {
        throw new Error("coverage state is invalid");
      }
      const reusable = interval.owner.kind === "reusable-environment";
      if ((reusable &&
           (!/^env_[A-Za-z0-9_-]{1,124}$/.test(interval.owner.environmentId || "") ||
            Boolean(interval.owner.sessionId))) ||
          (!reusable &&
           (!/^ses_[A-Za-z0-9_-]{1,124}$/.test(interval.owner.sessionId || "") ||
            Boolean(interval.owner.environmentId)))) {
        throw new Error("coverage owner is invalid");
      }
      if (interval.state === "Available" &&
          ((interval.droppedEventCount || 0) !== 0 || interval.retentionGap === true)) {
        throw new Error("coverage state falsely claims availability");
      }
    }
  }

  /** @param {Record<string, any>} finding */
  function validateRiskProjection(finding) {
    if (!isObject(finding) ||
        !RISK_ID_PATTERN.test(finding.id || "") ||
        !CODE_PATTERN.test(finding.ruleId || "") ||
        typeof finding.ruleVersion !== "string" ||
        finding.ruleVersion.length === 0 ||
        finding.ruleVersion.length > 64 ||
        finding.ruleVersion.trim() !== finding.ruleVersion ||
        finding.ruleVersion.includes("\u0000") ||
        !["info", "low", "medium", "high", "critical"].includes(finding.severity) ||
        typeof finding.title !== "string" ||
        finding.title.length === 0 ||
        finding.title.length > 256 ||
        typeof finding.explanation !== "string" ||
        finding.explanation.length === 0 ||
        finding.explanation.length > 2048 ||
        !["exact", "inferred", "limited"].includes(finding.confidence) ||
        !["allowed", "denied", "not-evaluated"].includes(finding.policyStatus) ||
        !validTimestamp(finding.firstAt) ||
        !validTimestamp(finding.lastAt) ||
        Date.parse(finding.lastAt) < Date.parse(finding.firstAt) ||
        !Number.isInteger(finding.count) ||
        finding.count < 1 ||
        !Array.isArray(finding.evidenceRefs || []) ||
        (finding.evidenceRefs || []).length > 256 ||
        (finding.evidenceRefs || []).some(
          (value) => !ACTIVITY_REF_PATTERN.test(value || "")
        ) ||
        (finding.nextAction &&
         !CODE_PATTERN.test(finding.nextAction))) {
      throw new Error("risk state is invalid");
    }
  }

  /** @param {Record<string, any>} capability */
  function validateCapabilityProjection(capability) {
    if (!isObject(capability) ||
        !CODE_PATTERN.test(capability.id || "") ||
        !["Available", "Partial", "Unavailable"].includes(capability.status) ||
        typeof (capability.provider || "") !== "string" ||
        (capability.provider || "").length > 128 ||
        typeof (capability.reason || "") !== "string" ||
        (capability.reason || "").length > 1024 ||
        typeof capability.mutable !== "boolean" ||
        !Array.isArray(capability.actionRefs || []) ||
        capability.actionRefs.length > 64 ||
        capability.actionRefs.some((value) => !CODE_PATTERN.test(value || ""))) {
      throw new Error("setting availability is invalid");
    }
  }

  /** @param {unknown} input @returns {ConsoleEvent} */
  function validateEvent(input) {
    if (!isObject(input)) throw new Error("event is missing");
    const event = /** @type {ConsoleEvent} */ (input);
    if (event.version !== EVENT_VERSION) {
      throw new Error(`unsupported event version "${event.version || ""}"`);
    }
    if (!INSTANCE_PATTERN.test(event.instanceId || "")) {
      throw new Error("event instanceId is invalid");
    }
    if (!Number.isInteger(event.credentialGeneration) ||
        event.credentialGeneration < 1) {
      throw new Error("event credentialGeneration must be positive");
    }
    if (!CODE_PATTERN.test(event.kind || "")) {
      throw new Error("event kind is invalid");
    }
    if (event.phase && !CODE_PATTERN.test(event.phase)) {
      throw new Error("event phase is invalid");
    }
    if (!Number.isInteger(event.seq) ||
        (event.kind === "terminal" ? event.seq !== 0 : event.seq <= 0)) {
      throw new Error(event.kind === "terminal" ?
        "terminal event must not consume broadcast sequence" :
        "broadcast event seq must be positive");
    }

    const entity = event.entity === undefined ? {} : event.entity;
    if (!isObject(entity) ||
        (entity.kind && !CODE_PATTERN.test(entity.kind))) {
      throw new Error("event entity kind is invalid");
    }
    for (const [name, value] of [
      ["entity.id", entity.id || ""],
      ["entity.profile", entity.profile || ""],
      ["entity.session", entity.session || ""]
    ]) {
      if (!boundedText(value, 128)) throw new Error(`${name} is invalid`);
    }

    if (!KNOWN_KINDS.has(event.kind)) {
      if (event.optional === true) return event;
      throw new Error(`unknown required event kind "${event.kind}"`);
    }

    const payload = isObject(event.payload) ? event.payload : {};
    for (const field of REQUIRED_FIELDS[event.kind] || []) {
      if (typeof payload[field] !== "string" || payload[field] === "") {
        throw new Error(`missing required field ${event.kind}.${field}`);
      }
    }
    if (event.kind === "lifecycle" &&
        (!isObject(payload.lifecycle) ||
         payload.lifecycle.schema !== "hideout.lifecycle-status/v1" ||
         typeof payload.lifecycle.environmentId !== "string" ||
         payload.lifecycle.environmentId === "")) {
      throw new Error("missing required field lifecycle.lifecycle");
    }

    switch (event.kind) {
      case "profile":
        validateProfileProjection(payload.profileProjection);
        break;
      case "transition":
        validateTransitionProjection(payload.transitionProjection);
        break;
      case "operation":
        validateOperationProjection(payload.operationProjection);
        break;
      case "activity":
        validateActivityProjection(payload.summary);
        break;
      case "coverage":
        validateCoverageProjection(payload.coverage);
        break;
      case "risk":
        validateRiskProjection(payload.riskProjection);
        break;
      case "capability":
        validateCapabilityProjection(payload.capabilityProjection);
        break;
    }
    return event;
  }

  /** @param {Array<Object>} activity */
  function activityCounts(activity) {
    const counts = new Map();
    for (const record of activity) {
      if (!isObject(record) || typeof record.kind !== "string") continue;
      const count = Number.isInteger(record.count) && record.count > 0 ?
        record.count : 0;
      counts.set(record.kind, (counts.get(record.kind) || 0) + count);
    }
    return Array.from(counts.entries())
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([kind, count]) => ({kind, count}));
  }

  /** @param {OperatorSnapshot} snapshot */
  function initializeEventViews(snapshot) {
    snapshot.transitions = [];
    snapshot.activitySummary = {
      cursor: snapshot.activityCursor || "",
      counts: activityCounts(snapshot.activity),
      recent: clone(snapshot.activity),
      retainedFrom: "",
      retainedTo: "",
      truncated: false
    };
    snapshot.auditTail = [];
    snapshot.deniedAuditTail = [];
    snapshot.background = [];
    snapshot.exportOutcomes = [];
    snapshot.cleanupOutcomes = [];
    snapshot.hostfsWrites = [];
    snapshot.decisions = [];
    snapshot.notices = [];
    snapshot.lifecycle = [];
  }

  /**
   * @param {OperatorSnapshot} input
   * @param {string=} profileScope
   * @returns {ConsoleState}
   */
  function seed(input, profileScope = "") {
    const snapshot = clone(validateSnapshot(input));
    initializeEventViews(snapshot);
    const live = LIVE_HEALTH.has(snapshot.streamHealth.state);
    const streamReadyHealth = live ? snapshot.streamHealth.state : "";
    const health = live ? {
      state: "seeding",
      reason: "waiting for authenticated event stream"
    } : {
      state: snapshot.streamHealth.state,
      reason: snapshot.streamHealth.reason || ""
    };
    snapshot.streamHealth = {
      state: health.state,
      ...(health.reason ? {reason: health.reason} : {})
    };
    return {
      version: SNAPSHOT_SCHEMA,
      snapshot,
      instanceId: snapshot.instanceId,
      credentialGeneration: snapshot.credentialGeneration,
      lastSeq: snapshot.sequence,
      profileScope,
      health,
      streamReadyHealth,
      readOnly: true,
      requiresReseed: false,
      diagnostics: []
    };
  }

  /**
   * An authoritative snapshot replaces every projection. It never attempts to
   * merge event-era state into the new daemon incarnation or credential epoch.
   *
   * @param {ConsoleState|null|undefined} previous
   * @param {OperatorSnapshot} input
   * @param {string=} profileScope
   * @returns {ConsoleState}
   */
  function reseed(previous, input, profileScope) {
    const scope = profileScope === undefined && previous ?
      previous.profileScope : (profileScope || "");
    return seed(input, scope);
  }

  /**
   * @param {string} health
   * @param {string} reason
   * @returns {ConsoleState}
   */
  function unavailable(health, reason) {
    const snapshot = {
      schema: SNAPSHOT_SCHEMA,
      instanceId: "",
      credentialGeneration: 0,
      sequence: 0,
      streamHealth: {state: health, reason},
      profiles: [],
      sessions: [],
      environments: [],
      activity: [],
      coverage: [],
      risks: [],
      operations: [],
      migrations: [],
      capabilities: [],
      nextActions: []
    };
    initializeEventViews(snapshot);
    return {
      version: SNAPSHOT_SCHEMA,
      snapshot,
      instanceId: "",
      credentialGeneration: 0,
      lastSeq: 0,
      profileScope: "",
      health: {state: health, reason},
      streamReadyHealth: "",
      readOnly: true,
      requiresReseed: true,
      diagnostics: reason ? [reason] : []
    };
  }

  /** @param {ConsoleState} state @returns {boolean} */
  function canMutate(state) {
    return Boolean(
      state &&
      state.version === SNAPSHOT_SCHEMA &&
      state.snapshot &&
      state.snapshot.schema === SNAPSHOT_SCHEMA &&
      !state.readOnly &&
      !state.requiresReseed &&
      LIVE_HEALTH.has(state.health.state) &&
      INSTANCE_PATTERN.test(state.instanceId || "") &&
      state.credentialGeneration > 0 &&
      state.lastSeq >= 0
    );
  }

  /** @param {ConsoleState} state @param {string} value */
  function appendDiagnostic(state, value) {
    if (!value) return;
    state.diagnostics = state.diagnostics.slice(-TAIL_LIMIT + 1);
    state.diagnostics.push(value);
  }

  /**
   * @param {ConsoleState} state
   * @param {string} health
   * @param {string} reason
   */
  function setHealth(state, health, reason) {
    state.health = {state: health, reason: reason || ""};
    state.snapshot.streamHealth = {
      state: health,
      ...(reason ? {reason} : {})
    };
  }

  /**
   * @param {ConsoleState} state
   * @param {string} health
   * @param {string} reason
   */
  function requireReseed(state, health, reason) {
    const message = reason || "current state must be refreshed";
    setHealth(state, health, message);
    appendDiagnostic(state, message);
    state.readOnly = true;
    state.requiresReseed = true;
    state.streamReadyHealth = "";
  }

  /**
   * Marks the snapshot mutable only after the sequence-bound EventSource has
   * opened. The server registers that subscriber before flushing HTTP 200, so
   * this transition cannot race a non-durable event.
   *
   * @param {ConsoleState} state
   * @returns {boolean}
   */
  function streamConnected(state) {
    if (!state || state.requiresReseed || state.health.state !== "seeding" ||
        !LIVE_HEALTH.has(state.streamReadyHealth)) {
      return false;
    }
    const health = state.streamReadyHealth;
    state.streamReadyHealth = "";
    setHealth(state, health, "");
    state.readOnly = false;
    return true;
  }

  /** @param {ConsoleState} state @param {string=} reason */
  function beginReseed(state, reason) {
    requireReseed(
      state,
      "seeding",
      reason || "refreshing verified state"
    );
  }

  /** @param {ConsoleState} state @param {string=} reason */
  function expireCredential(state, reason) {
    requireReseed(
      state,
      "credential-expired",
      reason || "operator credential expired"
    );
  }

  /**
   * Upserts have the same ordering as liveconsole: updates retain their slot;
   * new rows append, and a full projection drops its oldest row first.
   *
   * @param {Array<Object>} values
   * @param {Object} value
   * @param {string} key
   * @returns {Array<Object>}
   */
  function upsertProjection(values, value, key) {
    const id = value && value[key];
    if (!id) return values;
    const next = values.slice();
    const index = next.findIndex((row) => row && row[key] === id);
    if (index >= 0) {
      next[index] = clone(value);
      return next;
    }
    const retained = next.length >= PROJECTION_LIMIT ?
      next.slice(-PROJECTION_LIMIT + 1) : next;
    retained.push(clone(value));
    return retained;
  }

  /**
   * @param {Array<Object>} values
   * @param {Object} value
   * @param {string} key
   * @returns {Array<Object>}
   */
  function upsertUnbounded(values, value, key) {
    const id = value && value[key];
    if (!id) return values;
    const next = values.slice();
    const index = next.findIndex((row) => row && row[key] === id);
    if (index >= 0) next[index] = clone(value);
    else next.push(clone(value));
    return next;
  }

  /**
   * @param {Array<Object>} values
   * @param {Object} value
   * @param {string} key
   * @returns {Array<Object>}
   */
  function upsertTail(values, value, key) {
    const id = value && value[key];
    if (!id) return values;
    const next = values.slice();
    const index = next.findIndex((row) => row && row[key] === id);
    if (index >= 0) {
      next[index] = clone(value);
      return next;
    }
    next.unshift(clone(value));
    return next.slice(0, TAIL_LIMIT);
  }

  /** @param {Array<Object>} values @param {Object} value */
  function prependTail(values, value) {
    return [clone(value), ...values].slice(0, TAIL_LIMIT);
  }

  /** @param {Object} existing @param {Object} update */
  function mergeProjection(existing, update) {
    const merged = clone(existing);
    for (const [key, value] of Object.entries(update)) {
      if (value === undefined || value === null || value === "") continue;
      merged[key] = clone(value);
    }
    return merged;
  }

  /** @param {ConsoleState} state @param {Object} payload @param {Object} entity */
  function upsertEnvironment(state, payload, entity) {
    const row = clone(payload);
    row.id = row.id || entity.id;
    const values = state.snapshot.environments.slice();
    const index = values.findIndex((existing) =>
      existing.id === row.id ||
      (row.name && existing.name === row.name)
    );
    if (index >= 0) {
      values[index] = mergeProjection(values[index], row);
    } else if (row.id) {
      values.unshift(row);
    }
    state.snapshot.environments = values;
  }

  /** @param {ConsoleState} state @param {Object} payload @param {Object} entity */
  function upsertSession(state, payload, entity) {
    const id = payload.id || payload.session || entity.id;
    if (!id) return;
    const row = clone(payload);
    row.id = id;
    if (row.status && !row.state) row.state = row.status;
    const values = state.snapshot.sessions.slice();
    const index = values.findIndex((existing) => existing.id === id);
    if (index < 0) {
      values.push(row);
    } else {
      const merged = mergeProjection(values[index], row);
      merged.hasAudit = Boolean(values[index].hasAudit || row.hasAudit);
      merged.hasEphemeralState = Boolean(
        values[index].hasEphemeralState || row.hasEphemeralState
      );
      values[index] = merged;
    }
    state.snapshot.sessions = values;
  }

  /** @param {ConsoleState} state @param {Object} payload */
  function upsertWorkspaceView(state, payload) {
    const id = payload.session || payload.id;
    if (!id) return;
    const values = state.snapshot.sessions.slice();
    const index = values.findIndex((existing) => existing.id === id);
    const row = index >= 0 ? clone(values[index]) : {id};
    Object.assign(row, {
      profile: payload.profile || row.profile || "",
      environmentId: payload.environmentId || row.environmentId || "",
      workspaceId: payload.workspaceId,
      workspaceLabel: payload.workspaceLabel,
      guestWorkspace: payload.guestWorkspace,
      workspaceTransport: payload.workspaceTransport,
      workspaceViewState: payload.workspaceViewState,
      workspaceRelations: clone(payload.workspaceRelations || []),
      workspaceCleanupStatus: payload.cleanupStatus || "",
      workspaceBlockerCode: payload.blockerCode || ""
    });
    if (index >= 0) values[index] = row;
    else values.push(row);
    state.snapshot.sessions = values;

    if (!payload.environmentId) return;
    const activeStates = new Set([
      "provider-starting", "provider-ready", "view-mounting", "ready", "draining"
    ]);
    const active = values.filter((session) =>
      session.environmentId === payload.environmentId &&
      activeStates.has(session.workspaceViewState)
    ).length;
    state.snapshot.environments = state.snapshot.environments.map((environment) =>
      environment.id === payload.environmentId ?
        Object.assign({}, environment, {
          activeSessions: active,
          activeWorkspaceViews: active
        }) :
        environment
    );
  }

  /** @param {ConsoleState} state @param {Object} delta */
  function applyActivityProjection(state, delta) {
    const summary = state.snapshot.activitySummary || {
      cursor: "", counts: [], recent: [], retainedTo: ""
    };
    if (delta.cursor) {
      summary.cursor = delta.cursor;
      state.snapshot.activityCursor = delta.cursor;
    }
    for (const count of delta.counts || []) {
      summary.counts = upsertProjection(summary.counts, count, "kind");
    }
    if (delta.lastAt) summary.retainedTo = delta.lastAt;
    state.snapshot.activitySummary = summary;
  }

  /** @param {ConsoleState} state @param {Object} event */
  function eventMatchesProfile(state, event) {
    if (!state.profileScope) return true;
    const payload = event.payload || {};
    const profile = payload.profile ||
      (payload.profileProjection && payload.profileProjection.profile) ||
      (payload.transitionProjection && payload.transitionProjection.profile) ||
      (payload.summary && payload.summary.profile) ||
      (event.entity && event.entity.profile) ||
      "";
    return profile === "" || profile === state.profileScope;
  }

  /** @param {ConsoleState} state @param {ConsoleEvent} event */
  function reduceProjection(state, event) {
    const payload = event.payload || {};
    const entity = event.entity || {};
    switch (event.kind) {
      case "profile":
        state.snapshot.profiles = upsertProjection(
          state.snapshot.profiles,
          payload.profileProjection,
          "profile"
        );
        break;
      case "transition": {
        const projection = payload.transitionProjection;
        state.snapshot.transitions = upsertProjection(
          state.snapshot.transitions || [],
          projection,
          "profile"
        );
        const index = state.snapshot.profiles.findIndex(
          (profile) => profile.profile === projection.profile
        );
        if (index >= 0) {
          const profiles = state.snapshot.profiles.slice();
          profiles[index] = Object.assign({}, profiles[index], {
            transition: clone(projection.transition)
          });
          state.snapshot.profiles = profiles;
        }
        break;
      }
      case "operation":
        state.snapshot.operations = upsertProjection(
          state.snapshot.operations,
          payload.operationProjection,
          "id"
        );
        break;
      case "activity":
        applyActivityProjection(state, payload.summary);
        break;
      case "coverage":
        for (const interval of payload.coverage) {
          state.snapshot.coverage = upsertProjection(
            state.snapshot.coverage,
            interval,
            "id"
          );
        }
        break;
      case "risk":
        state.snapshot.risks = upsertProjection(
          state.snapshot.risks,
          payload.riskProjection,
          "id"
        );
        break;
      case "capability": {
        const capability = clone(payload.capabilityProjection);
        capability.state = capability.status.toLowerCase();
        state.snapshot.capabilities = upsertProjection(
          state.snapshot.capabilities,
          capability,
          "id"
        );
        break;
      }
      case "environment":
        upsertEnvironment(state, payload, entity);
        break;
      case "session":
        upsertSession(state, payload, entity);
        break;
      case "workspace-view":
        upsertWorkspaceView(state, payload);
        break;
      case "background":
        state.snapshot.background = upsertUnbounded(
          state.snapshot.background || [],
          {id: payload.id, op: payload.op, status: payload.status},
          "id"
        );
        break;
      case "audit": {
        const row = {
          time: payload.time,
          session: payload.session,
          profile: payload.profile,
          backend: payload.backend,
          action: payload.action,
          decision: payload.decision,
          details: clone(payload.details || {})
        };
        state.snapshot.auditTail = prependTail(
          state.snapshot.auditTail || [],
          row
        );
        if (payload.decision === "deny") {
          state.snapshot.deniedAuditTail = prependTail(
            state.snapshot.deniedAuditTail || [],
            row
          );
        }
        break;
      }
      case "export":
        state.snapshot.exportOutcomes = prependTail(
          state.snapshot.exportOutcomes || [],
          {
            status: payload.status,
            source: payload.source,
            artifactPath: payload.artifactPath,
            decision: payload.decision
          }
        );
        break;
      case "cleanup":
        state.snapshot.cleanupOutcomes = prependTail(
          state.snapshot.cleanupOutcomes || [],
          {
            status: payload.status,
            sessions: payload.sessions,
            removed: clone(payload.removed || []),
            secretState: payload.secretState
          }
        );
        break;
      case "hostfs-write":
        state.snapshot.hostfsWrites = upsertTail(
          state.snapshot.hostfsWrites || [],
          {
            decisionId: payload.decisionId,
            operationId: payload.operationId,
            profile: payload.profile,
            status: payload.status,
            operation: payload.operation,
            path: payload.path,
            destinationPath: payload.destinationPath,
            privilegeStatus: payload.privilegeStatus,
            reason: payload.reason
          },
          "decisionId"
        );
        break;
      case "decision":
        state.snapshot.decisions = upsertTail(
          state.snapshot.decisions || [],
          {
            id: payload.decisionId,
            kind: payload.recordKind,
            status: payload.status,
            defaultOutcome: payload.defaultOutcome,
            profile: payload.profile,
            session: payload.session,
            backend: payload.backend,
            reason: payload.reason,
            claimSurface: payload.claimSurface,
            claimOperator: payload.claimOperator,
            claimedAt: payload.claimedAt,
            claimExpiresAt: payload.claimExpiresAt,
            revision: payload.revision
          },
          "id"
        );
        break;
      case "notice":
        state.snapshot.notices = upsertTail(
          state.snapshot.notices || [],
          {
            id: payload.noticeId,
            kind: payload.recordKind,
            status: payload.status,
            severity: payload.severity,
            acknowledged: Boolean(payload.acknowledged),
            profile: payload.profile,
            session: payload.session,
            backend: payload.backend
          },
          "id"
        );
        break;
      case "lifecycle": {
        const lifecycle = payload.lifecycle;
        if (state.profileScope &&
            !state.snapshot.environments.some(
              (environment) => environment.id === lifecycle.environmentId
            )) {
          break;
        }
        state.snapshot.lifecycle = upsertUnbounded(
          state.snapshot.lifecycle || [],
          lifecycle,
          "environmentId"
        );
        break;
      }
    }
  }

  /** @param {unknown} error */
  function errorMessage(error) {
    return error && typeof error === "object" &&
      typeof error.message === "string" ?
      error.message : String(error);
  }

  /**
   * @param {ConsoleState} state
   * @param {unknown} input
   * @returns {{status:"applied"|"ignored"|"stale"|"error",reason?:string}}
   */
  function applyEvent(state, input) {
    if (!state || typeof state !== "object") {
      return {status: "error", reason: "nil state"};
    }
    if (state.requiresReseed) {
      return {status: "stale", reason: "current state must be refreshed"};
    }
    if (state.version !== SNAPSHOT_SCHEMA ||
        !state.snapshot ||
        state.snapshot.schema !== SNAPSHOT_SCHEMA) {
      const reason = "new event format requires fresh verified state";
      requireReseed(state, "schema-mismatch", reason);
      return {status: "stale", reason};
    }

    let event;
    try {
      event = validateEvent(input);
    } catch (error) {
      const reason = errorMessage(error);
      requireReseed(state, "schema-mismatch", reason);
      return {status: "stale", reason};
    }
    if (event.instanceId !== state.instanceId) {
      const reason = "daemon instance changed";
      requireReseed(state, "stale", reason);
      return {status: "stale", reason};
    }
    if (event.credentialGeneration !== state.credentialGeneration) {
      const reason = "stream sign-in credential changed";
      requireReseed(state, "credential-expired", reason);
      return {status: "stale", reason};
    }
    if (event.kind === "terminal") {
      const reason = event.payload.reason;
      const credentialReasons = new Set([
        "credential invalidated", "credential-invalidated",
        "credential expired", "credential-expired"
      ]);
      requireReseed(
        state,
        credentialReasons.has(reason) ? "credential-expired" : "disconnected",
        reason
      );
      return {status: "stale", reason};
    }
    if (event.seq <= state.lastSeq) {
      return {status: "ignored", reason: "old event"};
    }
    if (event.seq !== state.lastSeq + 1) {
      const reason = "event sequence gap";
      requireReseed(state, "stale", "event sequence gap");
      return {status: "stale", reason};
    }

    state.lastSeq = event.seq;
    state.snapshot.sequence = event.seq;
    state.streamReadyHealth = "";
    state.readOnly = false;
    state.requiresReseed = false;
    setHealth(state, "live", "");
    if (!KNOWN_KINDS.has(event.kind)) {
      appendDiagnostic(state, `ignored optional event kind ${event.kind}`);
      return {status: "ignored", reason: "unknown optional event kind"};
    }
    if (!eventMatchesProfile(state, event)) {
      return {status: "ignored", reason: "event outside profile scope"};
    }
    reduceProjection(state, event);
    return {status: "applied"};
  }

  /** @param {ConsoleState} state @param {string} reason */
  function disconnect(state, reason) {
    requireReseed(state, "disconnected", reason || "event stream closed");
  }

  root.State = Object.freeze({
    EVENT_VERSION,
    SNAPSHOT_SCHEMA,
    seed,
    reseed,
    unavailable,
    beginReseed,
    expireCredential,
    streamConnected,
    applyEvent,
    canMutate,
    disconnect
  });
})();
