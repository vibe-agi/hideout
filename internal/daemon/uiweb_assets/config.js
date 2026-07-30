// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole = window.HideoutConsole || {};

  const DRAFT_SCHEMA = "hideout.configuration-draft.v1";
  const PLAN_SCHEMA = "hideout.configuration-plan.v1";
  const APPLY_SCHEMA = "hideout.configuration-apply.v1";
  const OPERATION_SCHEMA = "hideout.operation.v1";
  const DIGEST_DOMAIN = "configuration-plan";
  const CANONICAL_VERSION = "hideout.canonical-json/v1";

  const STAGE_EDITING = "editing-draft";
  const STAGE_PLANNING = "planning";
  const STAGE_REVIEW = "review";
  const STAGE_CONFIRMING = "confirming";
  const STAGE_APPLYING = "applying";
  const STAGE_TERMINAL = "terminal";
  const STAGE_STALE = "stale";
  const STAGE_ERROR = "error";

  const CONTROL_PATTERN =
    /[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/u;
  const PROFILE_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
  const OPERATION_PATTERN = /^op_[A-Za-z0-9_-]{8,124}$/;
  const DIGEST_PATTERN = /^sha256:[a-f0-9]{64}$/;
  const ENVIRONMENT_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;
  const SECRET_REF_PATTERN = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;
  const TERMINAL_PHASES = new Set([
    "succeeded", "failed", "cancelled", "rolled-back",
    "rollback-unproved"
  ]);

  const DEFINITIONS = Object.freeze([
    Object.freeze({
      kind: "network.posture",
      capability: "config.network.posture",
      label: "Connection mode",
      scope: "live · new connections",
      description:
        "Choose direct or mediated proxy routing. A proxy plan also needs a " +
        "managed proxy reference and mediated DNS in the same local draft."
    }),
    Object.freeze({
      kind: "network.proxyRef",
      capability: "config.network.proxy-ref",
      label: "Proxy secret reference",
      scope: "live · new connections",
      description:
        "Select the name of a daemon-managed secret. Secret bytes never enter " +
        "this profile draft or its review."
    }),
    Object.freeze({
      kind: "network.dns",
      capability: "config.network.dns",
      label: "DNS mediation",
      scope: "live · new connections",
      description:
        "Use the system resolver or carry DNS through an IP-backed mediated " +
        "resolver. Manager performs authoritative validation."
    }),
    Object.freeze({
      kind: "profile.environment",
      capability: "config.profile.environment",
      label: "Environment policy",
      scope: "new sessions",
      description:
        "Set or remove public values, explicit inheritance, and deny patterns " +
        "for future session snapshots. Set values are hidden in review."
    }),
    Object.freeze({
      kind: "profile.hostfs",
      capability: "config.profile.hostfs",
      label: "Host file access",
      scope: "new sessions",
      description:
        "Add an allow or deny rule, or remove an exact rule ID. Expanding host " +
        "file authority is highlighted as high risk during review."
    }),
    Object.freeze({
      kind: "profile.commandProxy",
      capability: "config.profile.command-proxy",
      label: "Command proxy",
      scope: "new sessions",
      description:
        "Add a host.open-compatible command shim or remove an optional shim " +
        "from future sessions."
    }),
    Object.freeze({
      kind: "profile.commandAdapter",
      capability: "config.profile.command-adapter",
      label: "Command adapters",
      scope: "new sessions",
      description:
        "Add, enable, disable, refresh, or remove an exact command adapter. " +
        "Local adapter source is verified by Manager."
    }),
    Object.freeze({
      kind: "activity.retention",
      capability: "config.activity.retention",
      label: "Activity retention",
      scope: "future activity owners",
      description:
        "Set bounded metadata retention for future exact owners. Zero age " +
        "means retain for the environment or disposable-run lifecycle."
    })
  ]);

  let nonceCounter = 0;

  /** @param {unknown} value */
  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  /** @param {Object} snapshot @param {string} profile */
  function projection(snapshot, profile) {
    return (snapshot && snapshot.profiles || []).find(
      (value) => value && value.profile === profile
    ) || null;
  }

  /** @param {string} kind */
  function definition(kind) {
    return DEFINITIONS.find((value) => value.kind === kind) || null;
  }

  /** @param {Object} snapshot @param {string} capabilityID */
  function capability(snapshot, capabilityID) {
    return (snapshot && snapshot.capabilities || []).find(
      (value) => value && value.id === capabilityID
    ) || null;
  }

  /** @param {Object} snapshot @param {string} profile */
  function createDraft(snapshot, profile) {
    const current = projection(snapshot, profile);
    if (!current ||
        !Number.isSafeInteger(current.revision) ||
        current.revision < 1 ||
        !PROFILE_PATTERN.test(profile)) {
      throw new Error("authoritative profile projection is unavailable");
    }
    nonceCounter++;
    return {
      schema: DRAFT_SCHEMA,
      profile,
      baseRevision: current.revision,
      clientNonce:
        `browser-${current.revision}-${Date.now().toString(36)}-` +
        nonceCounter.toString(36),
      changes: []
    };
  }

  /** @param {Object} draft @param {string} kind @param {Object} value */
  function withChange(draft, kind, value) {
    if (!draft ||
        draft.schema !== DRAFT_SCHEMA ||
        !definition(kind) ||
        !value ||
        typeof value !== "object" ||
        Array.isArray(value)) {
      throw new Error("configuration change is invalid");
    }
    const next = clone(draft);
    next.changes = next.changes.filter((change) => change.kind !== kind);
    next.changes.push({kind, value: clone(value)});
    next.changes.sort((left, right) => left.kind.localeCompare(right.kind));
    return next;
  }

  /** @param {Object} draft @param {string} kind */
  function withoutChange(draft, kind) {
    const next = clone(draft);
    next.changes = (next.changes || []).filter(
      (change) => change.kind !== kind
    );
    return next;
  }

  /**
   * Merge the one typed change whose wire shape supports multiple independent
   * operations. Every other kind remains replace-only because Manager rejects
   * duplicate discriminators in one draft.
   *
   * @param {Object} draft
   * @param {string} kind
   * @param {Object} value
   */
  function withMergedChange(draft, kind, value) {
    if (kind !== "profile.environment") {
      return withChange(draft, kind, value);
    }
    const existing = (draft.changes || []).find(
      (change) => change.kind === kind
    );
    const merged = existing ? clone(existing.value) : {};
    for (const [name, entry] of Object.entries(value.set || {})) {
      merged.set = Object.assign({}, merged.set || {}, {[name]: entry});
      merged.unset = (merged.unset || []).filter((item) => item !== name);
    }
    for (const name of value.unset || []) {
      merged.unset = unique([...(merged.unset || []), name]);
      if (merged.set) delete merged.set[name];
    }
    mergeOpposedLists(merged, value, "inherit", "uninherit");
    mergeOpposedLists(merged, value, "deny", "undeny");
    for (const key of [
      "set", "unset", "inherit", "uninherit", "deny", "undeny"
    ]) {
      if (Array.isArray(merged[key]) && merged[key].length === 0) {
        delete merged[key];
      }
      if (key === "set" &&
          merged.set &&
          Object.keys(merged.set).length === 0) {
        delete merged.set;
      }
    }
    return withChange(draft, kind, merged);
  }

  /**
   * @param {Object} target
   * @param {Object} addition
   * @param {string} add
   * @param {string} remove
   */
  function mergeOpposedLists(target, addition, add, remove) {
    for (const value of addition[add] || []) {
      target[add] = unique([...(target[add] || []), value]);
      target[remove] = (target[remove] || []).filter(
        (item) => item !== value
      );
    }
    for (const value of addition[remove] || []) {
      target[remove] = unique([...(target[remove] || []), value]);
      target[add] = (target[add] || []).filter((item) => item !== value);
    }
  }

  /** @param {string[]} values */
  function unique(values) {
    return [...new Set(values)].sort();
  }

  /** @param {Object} draft */
  function draftFingerprint(draft) {
    const copy = clone(draft);
    copy.changes = (copy.changes || []).slice().sort(
      (left, right) => left.kind.localeCompare(right.kind)
    );
    return canonicalJSON(copy);
  }

  /** @param {unknown} value */
  function canonicalJSON(value) {
    if (value === null) return "null";
    switch (typeof value) {
    case "boolean":
      return value ? "true" : "false";
    case "number":
      if (!Number.isFinite(value)) {
        throw new Error("canonical JSON contains a non-finite number");
      }
      return JSON.stringify(value);
    case "string":
      return JSON.stringify(value)
        .replace(/\u2028/g, "\\u2028")
        .replace(/\u2029/g, "\\u2029");
    case "object":
      if (Array.isArray(value)) {
        return "[" + value.map(canonicalJSON).join(",") + "]";
      }
      return "{" + Object.keys(value).sort().map((key) => {
        if (value[key] === undefined) {
          throw new Error("canonical JSON contains undefined");
        }
        return canonicalJSON(key) + ":" + canonicalJSON(value[key]);
      }).join(",") + "}";
    default:
      throw new Error("canonical JSON contains an unsupported value");
    }
  }

  /** @param {Object} plan */
  async function canonicalPlanDigest(plan) {
    if (!window.crypto ||
        !window.crypto.subtle ||
        !window.TextEncoder) {
      throw new Error("browser SHA-256 support is unavailable");
    }
    const value = clone(plan);
    value.planDigest = "";
    const input = new window.TextEncoder().encode(
      CANONICAL_VERSION + "\0" + DIGEST_DOMAIN + "\0" +
      canonicalJSON(value)
    );
    const digest = await window.crypto.subtle.digest("SHA-256", input);
    const bytes = new Uint8Array(digest);
    let hex = "";
    for (const byte of bytes) hex += byte.toString(16).padStart(2, "0");
    return "sha256:" + hex;
  }

  /** @param {unknown} value @param {string} label @param {number} max */
  function requiredText(value, label, max) {
    const text = String(value === undefined || value === null ? "" : value);
    if (!text ||
        text.trim() !== text ||
        text.length > max ||
        CONTROL_PATTERN.test(text)) {
      throw new Error(`${label} is invalid`);
    }
    return text;
  }

  /** @param {unknown} value @param {string} label @param {number} max */
  function optionalText(value, label, max) {
    const text = String(value === undefined || value === null ? "" : value);
    if (text &&
        (text.trim() !== text ||
         text.length > max ||
         CONTROL_PATTERN.test(text))) {
      throw new Error(`${label} is invalid`);
    }
    return text;
  }

  /** @param {unknown} value @param {string} label */
  function environmentName(value, label) {
    const name = String(value || "");
    if (!ENVIRONMENT_NAME_PATTERN.test(name)) {
      throw new Error(`${label} is invalid`);
    }
    return name;
  }

  /** @param {unknown} raw @param {string} label */
  function list(raw, label) {
    const values = String(raw || "")
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    for (const value of values) requiredText(value, label, 256);
    return unique(values);
  }

  /** @param {unknown} value @param {string} label */
  function boundedInteger(value, label) {
    const number = Number(value);
    if (!Number.isSafeInteger(number)) {
      throw new Error(`${label} must be a whole number`);
    }
    return number;
  }

  /**
   * Convert static form values into one member of Manager's closed typed-change
   * union. This validation improves feedback only; Manager remains authoritative.
   *
   * @param {string} kind
   * @param {Record<string, unknown>} input
   */
  function buildChange(kind, input) {
    switch (kind) {
    case "network.posture": {
      const mode = String(input.mode || "");
      if (mode !== "direct" && mode !== "proxy") {
        throw new Error("connection mode must be direct or proxy");
      }
      return {mode};
    }
    case "network.proxyRef": {
      const ref = String(input.ref || "");
      if (!SECRET_REF_PATTERN.test(ref) || ref.length > 64) {
        throw new Error(
          "proxy reference must use lowercase letters, digits, and inner hyphens"
        );
      }
      return {ref};
    }
    case "network.dns": {
      const mode = String(input.mode || "");
      if (!["system", "ip", "doh"].includes(mode)) {
        throw new Error("DNS mode must be system, ip, or doh");
      }
      if (mode === "system") return {mode};
      const serverIp = requiredText(input.serverIp, "DNS server IP", 128);
      return {mode, serverIp};
    }
    case "profile.environment": {
      const operation = String(input.operation || "");
      if (operation === "set") {
        const name = environmentName(input.name, "environment name");
        const value = String(
          input.value === undefined || input.value === null ? "" : input.value
        );
        if (value.length > 8192 || CONTROL_PATTERN.test(value)) {
          throw new Error("environment value is invalid");
        }
        return {set: {[name]: value}};
      }
      if (["unset", "inherit", "uninherit"].includes(operation)) {
        const name = environmentName(input.name, "environment name");
        return {[operation]: [name]};
      }
      if (["deny", "undeny"].includes(operation)) {
        const pattern = requiredText(input.name, "environment pattern", 128);
        return {[operation]: [pattern]};
      }
      throw new Error("environment operation is unsupported");
    }
    case "profile.hostfs": {
      const operation = String(input.operation || "");
      if (operation === "remove") {
        return {
          operation,
          ruleId: requiredText(input.ruleId, "HostFS rule ID", 128)
        };
      }
      if (operation === "add" || operation === "deny") {
        return {
          operation,
          rule: requiredText(input.rule, "HostFS rule", 4096),
          reason: requiredText(input.reason, "HostFS reason", 1024)
        };
      }
      throw new Error("HostFS operation must be add, deny, or remove");
    }
    case "profile.commandProxy": {
      const operation = String(input.operation || "");
      if (operation !== "add-open" && operation !== "remove") {
        throw new Error("command proxy operation is unsupported");
      }
      return {
        operation,
        command: requiredText(input.command, "command proxy name", 128)
      };
    }
    case "profile.commandAdapter": {
      const operation = String(input.operation || "");
      const adapterId = requiredText(input.adapterId, "adapter ID", 128);
      if (operation === "add-local") {
        const result = {
          operation,
          adapterId,
          path: requiredText(input.path, "adapter path", 4096),
          commands: list(input.commands, "adapter commands")
        };
        if (!result.commands.length) {
          throw new Error("local adapter needs at least one command");
        }
        const entrypoint = optionalText(
          input.entrypoint,
          "adapter entrypoint",
          256
        );
        if (entrypoint) result.entrypoint = entrypoint;
        const allowed = list(
          input.allowedProposalCapabilities,
          "proposal capability"
        );
        if (allowed.length) result.allowedProposalCapabilities = allowed;
        return result;
      }
      if (operation === "add-builtin-root-sensitive" ||
          ["enable", "disable", "refresh-digest", "remove"].includes(
            operation
          )) {
        return {operation, adapterId};
      }
      throw new Error("command adapter operation is unsupported");
    }
    case "activity.retention": {
      const maxBytes = boundedInteger(input.maxBytes, "retention bytes");
      const maxAgeSeconds = boundedInteger(
        input.maxAgeSeconds,
        "retention age"
      );
      if (maxBytes < 1024 ||
          maxBytes > 10 * 1024 * 1024 * 1024 ||
          maxAgeSeconds < 0 ||
          maxAgeSeconds > 365 * 24 * 60 * 60) {
        throw new Error("activity retention bounds are invalid");
      }
      return {maxBytes, maxAgeSeconds};
    }
    default:
      throw new Error("configuration change kind is unsupported");
    }
  }

  /** @param {string} kind @param {Object} current */
  function formModel(kind, current) {
    const desired = current && current.desired || {};
    const network = desired.network || {};
    const activity = desired.activity && desired.activity.retention || {
      maxBytes: 256 * 1024 * 1024,
      maxAgeSeconds: 0
    };
    const common = definition(kind);
    if (!common) throw new Error("configuration editor is unsupported");
    const model = {
      kind,
      title: common.label,
      description: common.description,
      scope: common.scope,
      fields: []
    };
    switch (kind) {
    case "network.posture":
      model.fields = [{
        name: "mode", label: "Desired connection mode", type: "select",
        value: network.mode === "tun2socks" ? "proxy" : "direct",
        options: [
          {value: "direct", label: "Direct"},
          {value: "proxy", label: "Managed proxy"}
        ],
        help: "Proxy requires proxy reference and mediated DNS in this draft."
      }];
      break;
    case "network.proxyRef":
      model.fields = [{
        name: "ref", label: "Managed secret reference", type: "text",
        value: network.proxySecretRef || "", placeholder: "local-proxy",
        help: "This is a reference name, never a socks5:// URL or secret value."
      }];
      break;
    case "network.dns":
      model.fields = [{
        name: "mode", label: "DNS mode", type: "select",
        value: network.mediatedResolver ? "doh" : "system",
        options: [
          {value: "system", label: "System resolver"},
          {value: "doh", label: "DNS over HTTPS carrier"},
          {value: "ip", label: "IP resolver carrier"}
        ]
      }, {
        name: "serverIp", label: "Resolver IP literal", type: "text",
        value: network.mediatedResolver || "", placeholder: "1.1.1.1",
        help: "Ignored only when DNS mode is system."
      }];
      break;
    case "profile.environment":
      model.fields = [{
        name: "operation", label: "Operation", type: "select", value: "set",
        options: [
          {value: "set", label: "Set public value"},
          {value: "unset", label: "Remove public value"},
          {value: "inherit", label: "Allow explicit inheritance"},
          {value: "uninherit", label: "Remove inheritance"},
          {value: "deny", label: "Add deny pattern"},
          {value: "undeny", label: "Remove deny pattern"}
        ]
      }, {
        name: "name", label: "Name or deny pattern", type: "text", value: "",
        placeholder: "HTTPS_PROXY"
      }, {
        name: "value", label: "Public value (set only)", type: "textarea",
        value: "",
        help:
          "Use managed secrets for credentials. Manager hides this value in review."
      }];
      break;
    case "profile.hostfs":
      model.fields = [{
        name: "operation", label: "Operation", type: "select", value: "add",
        options: [
          {value: "add", label: "Add allow rule"},
          {value: "deny", label: "Add deny rule"},
          {value: "remove", label: "Remove exact rule ID"}
        ]
      }, {
        name: "rule", label: "Rule specification", type: "text", value: "",
        placeholder: "/workspace:rw"
      }, {
        name: "reason", label: "Reason", type: "text", value: "",
        placeholder: "Needed for project sources"
      }, {
        name: "ruleId", label: "Rule ID (remove only)", type: "text", value: "",
        placeholder: "hfs_…"
      }];
      break;
    case "profile.commandProxy":
      model.fields = [{
        name: "operation", label: "Operation", type: "select",
        value: "add-open",
        options: [
          {value: "add-open", label: "Add host.open shim"},
          {value: "remove", label: "Remove optional shim"}
        ]
      }, {
        name: "command", label: "Command name", type: "text", value: "",
        placeholder: "browser-open"
      }];
      break;
    case "profile.commandAdapter":
      model.fields = [{
        name: "operation", label: "Operation", type: "select",
        value: "enable",
        options: [
          {value: "enable", label: "Enable"},
          {value: "disable", label: "Disable"},
          {value: "refresh-digest", label: "Refresh verified digest"},
          {value: "remove", label: "Remove"},
          {value: "add-local", label: "Add local adapter"},
          {
            value: "add-builtin-root-sensitive",
            label: "Add built-in root-sensitive intent adapter"
          }
        ]
      }, {
        name: "adapterId", label: "Adapter ID", type: "text", value: "",
        placeholder: "safe-tool"
      }, {
        name: "path", label: "Local source path (add-local)", type: "text",
        value: "", placeholder: "adapters/safe-tool.js"
      }, {
        name: "entrypoint", label: "Entrypoint (optional)", type: "text",
        value: "", placeholder: "main.js"
      }, {
        name: "commands", label: "Commands, comma separated", type: "text",
        value: "", placeholder: "tool, tool-safe"
      }, {
        name: "allowedProposalCapabilities",
        label: "Allowed proposal capabilities, comma separated",
        type: "text", value: "", placeholder: "host.open"
      }];
      break;
    case "activity.retention":
      model.fields = [{
        name: "maxBytes", label: "Maximum bytes", type: "number",
        value: String(activity.maxBytes), min: "1024",
        max: String(10 * 1024 * 1024 * 1024), step: "1"
      }, {
        name: "maxAgeSeconds", label: "Maximum age in seconds", type: "number",
        value: String(activity.maxAgeSeconds), min: "0",
        max: String(365 * 24 * 60 * 60), step: "1",
        help: "0 retains metadata for the exact owner lifecycle."
      }];
      break;
    }
    return model;
  }

  /** @param {Object} snapshot @param {string} profile */
  function createTransaction(snapshot, profile) {
    const current = projection(snapshot, profile);
    const draft = createDraft(snapshot, profile);
    return {
      stage: STAGE_EDITING,
      stageBeforeStale: "",
      draft,
      baseDigest: current.contentDigest || "",
      reviewedDraftFingerprint: "",
      plan: null,
      operation: null,
      resultProjection: null,
      error: "",
      authorityReason: "",
      responseLost: false
    };
  }

  /**
   * @param {Object} transaction
   * @param {string} kind
   * @param {Object} value
   */
  function editTransaction(transaction, kind, value) {
    if (!transaction ||
        ![STAGE_EDITING, STAGE_REVIEW, STAGE_ERROR].includes(
          transaction.stage
        )) {
      throw new Error("configuration draft is not editable");
    }
    const next = clone(transaction);
    next.draft = withMergedChange(next.draft, kind, value);
    next.stage = STAGE_EDITING;
    next.stageBeforeStale = "";
    next.reviewedDraftFingerprint = "";
    next.plan = null;
    next.operation = null;
    next.resultProjection = null;
    next.error = "";
    next.authorityReason = "";
    next.responseLost = false;
    return next;
  }

  /** @param {Object} transaction @param {string} kind */
  function removeTransactionChange(transaction, kind) {
    const next = clone(transaction);
    next.draft = withoutChange(next.draft, kind);
    next.stage = STAGE_EDITING;
    next.reviewedDraftFingerprint = "";
    next.plan = null;
    next.operation = null;
    next.resultProjection = null;
    next.error = "";
    next.responseLost = false;
    return next;
  }

  /**
   * @param {Object} transaction
   * @param {Object} snapshot
   * @param {boolean} mutable
   * @param {string=} reason
   * @param {Date=} now
   */
  function sync(
    transaction,
    snapshot,
    mutable,
    reason,
    now
  ) {
    if (!transaction ||
        transaction.stage === STAGE_APPLYING ||
        transaction.stage === STAGE_TERMINAL) {
      return transaction;
    }
    const next = clone(transaction);
    const current = projection(snapshot, next.draft.profile);
    const sameBase = Boolean(
      current &&
      current.revision === next.draft.baseRevision &&
      current.contentDigest === next.baseDigest
    );
    const expired = Boolean(
      next.plan &&
      !isBefore(now || new Date(), next.plan.expiresAt)
    );
    if (!mutable || !sameBase || expired) {
      if (next.stage !== STAGE_STALE) {
        next.stageBeforeStale = next.stage;
      }
      next.stage = STAGE_STALE;
      next.authorityReason = !sameBase ?
        "Profile revision changed; discard this draft and review a fresh projection." :
        expired ?
          "The reviewed plan expired; discard it and request a fresh plan." :
          reason || "Authenticated mutation authority is unavailable.";
      return next;
    }
    if (next.stage === STAGE_STALE &&
        (next.stageBeforeStale === STAGE_EDITING ||
         !next.stageBeforeStale)) {
      next.stage = STAGE_EDITING;
      next.stageBeforeStale = "";
      next.authorityReason = "";
    }
    return next;
  }

  /**
   * @param {Object} transaction
   * @param {Object} current
   * @param {boolean} mutable
   * @param {Date=} now
   */
  function confirmability(transaction, current, mutable, now) {
    const reasons = [];
    if (!transaction || transaction.stage !== STAGE_REVIEW) {
      reasons.push("No reviewed plan is ready.");
      return {allowed: false, reasons};
    }
    const plan = transaction.plan;
    if (!mutable) reasons.push("Console state is not live and mutable.");
    if (!plan) reasons.push("Manager plan is unavailable.");
    if (!current ||
        current.profile !== transaction.draft.profile ||
        current.revision !== transaction.draft.baseRevision ||
        current.contentDigest !== transaction.baseDigest) {
      reasons.push("The profile projection no longer matches the reviewed base.");
    }
    if (plan && !isBefore(now || new Date(), plan.expiresAt)) {
      reasons.push("The Manager plan has expired.");
    }
    if (plan && Array.isArray(plan.blockers) && plan.blockers.length) {
      reasons.push("The reviewed plan has unresolved blockers.");
    }
    if (transaction.reviewedDraftFingerprint !==
        draftFingerprint(transaction.draft)) {
      reasons.push("The local draft changed after review.");
    }
    return {allowed: reasons.length === 0, reasons};
  }

  /** @param {Date} now @param {string} expiresAt */
  function isBefore(now, expiresAt) {
    const expiry = new Date(expiresAt);
    return Number.isFinite(expiry.getTime()) && now.getTime() < expiry.getTime();
  }

  /** @param {Object} transaction */
  function startReview(transaction) {
    if (!transaction ||
        transaction.stage !== STAGE_EDITING ||
        !transaction.draft.changes.length) {
      throw new Error("add at least one local change before review");
    }
    const next = clone(transaction);
    next.stage = STAGE_PLANNING;
    next.reviewedDraftFingerprint = draftFingerprint(next.draft);
    next.plan = null;
    next.operation = null;
    next.error = "";
    next.responseLost = false;
    return next;
  }

  /**
   * @param {{configurationPlan:(draft:Object)=>Promise<Object>}} client
   * @param {Object} transaction
   */
  async function finishReview(client, transaction) {
    if (!transaction || transaction.stage !== STAGE_PLANNING) {
      throw new Error("configuration transaction is not planning");
    }
    const next = clone(transaction);
    try {
      const plan = await client.configurationPlan(clone(next.draft));
      validatePlan(plan, next);
      const expectedDigest = await canonicalPlanDigest(plan);
      if (expectedDigest !== plan.planDigest) {
        throw new Error("Manager configuration plan digest mismatch");
      }
      if (next.reviewedDraftFingerprint !== draftFingerprint(next.draft)) {
        throw new Error("local draft changed while Manager was planning");
      }
      next.plan = clone(plan);
      next.stage = STAGE_REVIEW;
      next.error = "";
      return next;
    } catch (error) {
      next.stage = error && (
        error.code === "stale-draft" ||
        error.code === "stale-plan"
      ) ? STAGE_STALE : STAGE_ERROR;
      next.stageBeforeStale = STAGE_PLANNING;
      next.error = `Manager could not create a matching plan: ${String(error)}`;
      next.authorityReason = next.stage === STAGE_STALE ?
        "Manager rejected the stale profile revision; refresh before reviewing." :
        "";
      return next;
    }
  }

  /** @param {Object} plan @param {Object} transaction */
  function validatePlan(plan, transaction) {
    if (!plan ||
        plan.schema !== PLAN_SCHEMA ||
        !OPERATION_PATTERN.test(plan.operationId || "") ||
        !DIGEST_PATTERN.test(plan.planDigest || "") ||
        !DIGEST_PATTERN.test(plan.baseDigest || "") ||
        plan.profile !== transaction.draft.profile ||
        plan.baseRevision !== transaction.draft.baseRevision ||
        plan.baseDigest !== transaction.baseDigest ||
        !Array.isArray(plan.canonicalChanges) ||
        !Array.isArray(plan.diff) ||
        !Array.isArray(plan.effects) ||
        !Array.isArray(plan.blockers) ||
        !Array.isArray(plan.warnings) ||
        !plan.rollback ||
        !Array.isArray(plan.rollback.effects) ||
        !isBefore(new Date(0), plan.expiresAt)) {
      throw new Error("Manager returned an invalid configuration plan");
    }
    for (const collection of [
      plan.canonicalChanges,
      plan.diff,
      plan.effects,
      plan.blockers,
      plan.warnings
    ]) {
      if (collection.length > 256) {
        throw new Error("Manager plan exceeds browser review bounds");
      }
    }
    const expectedKinds = transaction.draft.changes.map(
      (change) => change.kind
    ).sort();
    const actualKinds = plan.canonicalChanges.map((change) => {
      if (!definition(change.kind) ||
          !change.value ||
          typeof change.value !== "object") {
        throw new Error("Manager returned an unknown canonical change");
      }
      return change.kind;
    }).sort();
    if (canonicalJSON(expectedKinds) !== canonicalJSON(actualKinds)) {
      throw new Error("Manager plan does not bind the exact local draft");
    }
    for (const diff of plan.diff) {
      if (!diff ||
          !definition(diff.kind) ||
          !diff.field ||
          !diff.scope ||
          String(diff.field).length > 256 ||
          String(diff.before || "").length > 2048 ||
          String(diff.after || "").length > 2048) {
        throw new Error("Manager returned an invalid review diff");
      }
    }
    for (const effect of plan.effects) {
      if (!effect ||
          !effect.effectId ||
          !effect.kind ||
          !effect.scope ||
          !effect.provider ||
          !effect.summary ||
          !Array.isArray(effect.proofRequired)) {
        throw new Error("Manager returned an invalid planned effect");
      }
    }
    for (const blocker of plan.blockers) {
      if (!blocker || !blocker.code || !blocker.summary || !blocker.recovery) {
        throw new Error("Manager returned an invalid blocker");
      }
    }
    for (const warning of plan.warnings) {
      if (!warning || !warning.code || !warning.summary) {
        throw new Error("Manager returned an invalid warning");
      }
    }
    if (!plan.rollback.mode || !plan.rollback.summary) {
      throw new Error("Manager returned an invalid rollback plan");
    }
  }

  /**
   * @param {Object} transaction
   * @param {Object} current
   * @param {boolean} mutable
   * @param {Date=} now
   */
  function startConfirmation(transaction, current, mutable, now) {
    const status = confirmability(transaction, current, mutable, now);
    if (!status.allowed) throw new Error(status.reasons.join(" "));
    const next = clone(transaction);
    next.stage = STAGE_CONFIRMING;
    next.error = "";
    return next;
  }

  /** @param {Object} plan */
  function highRisk(plan) {
    if (!plan) return false;
    for (const warning of plan.warnings || []) {
      const code = String(warning.code || "").toLowerCase();
      if (code.includes("authority-expanded") ||
          code.includes("root-sensitive") ||
          code.includes("destructive")) {
        return true;
      }
    }
    return (plan.effects || []).some((effect) => effect.kind === "cleanup");
  }

  /**
   * @param {Object} transaction
   * @param {Object} current
   * @param {boolean} mutable
   * @param {string=} typedProfile
   * @param {Date=} now
   */
  function startApply(
    transaction,
    current,
    mutable,
    typedProfile,
    now
  ) {
    if (!transaction ||
        transaction.stage !== STAGE_CONFIRMING ||
        !transaction.plan) {
      throw new Error("configuration plan is not awaiting confirmation");
    }
    const probe = clone(transaction);
    probe.stage = STAGE_REVIEW;
    const status = confirmability(probe, current, mutable, now);
    if (!status.allowed) throw new Error(status.reasons.join(" "));
    if (highRisk(transaction.plan) &&
        typedProfile !== transaction.plan.profile) {
      throw new Error("typed confirmation does not match the exact profile");
    }
    const next = clone(transaction);
    next.stage = STAGE_APPLYING;
    next.error = "";
    next.responseLost = false;
    return next;
  }

  /**
   * @param {{
   *   configurationApply:(request:Object)=>Promise<Object>,
   *   operation:(id:string)=>Promise<Object>
   * }} client
   * @param {Object} transaction
   */
  async function finishApply(client, transaction) {
    if (!transaction ||
        transaction.stage !== STAGE_APPLYING ||
        !transaction.plan) {
      throw new Error("configuration transaction is not applying");
    }
    const next = clone(transaction);
    const plan = next.plan;
    const request = {
      schema: APPLY_SCHEMA,
      operationId: plan.operationId,
      profile: plan.profile,
      baseRevision: plan.baseRevision,
      planDigest: plan.planDigest,
      confirmed: true
    };
    try {
      const result = await client.configurationApply(request);
      validateApplyResult(result, plan);
      next.operation = clone(result.operation);
      next.resultProjection = result.projection &&
        result.projection.profile ? clone(result.projection) : null;
      next.stage = STAGE_TERMINAL;
      next.responseLost = !isTerminalOperation(result.operation);
      next.error = "";
      return next;
    } catch (applyError) {
      next.stage = STAGE_TERMINAL;
      next.responseLost = true;
      next.error =
        `Apply response was not authoritative: ${String(applyError)}`;
      try {
        const operation = await client.operation(plan.operationId);
        validateOperation(operation, plan);
        next.operation = clone(operation);
        next.responseLost = !isTerminalOperation(operation);
        if (!next.responseLost) next.error = "";
      } catch (lookupError) {
        next.error +=
          ` Operation lookup also failed: ${String(lookupError)}`;
      }
      return next;
    }
  }

  /** @param {Object} result @param {Object} plan */
  function validateApplyResult(result, plan) {
    if (!result || !result.operation) {
      throw new Error("Manager returned no durable operation");
    }
    validateOperation(result.operation, plan);
    if (result.projection && result.projection.profile) {
      if (result.projection.profile !== plan.profile ||
          !Number.isSafeInteger(result.projection.revision) ||
          result.projection.revision < plan.baseRevision) {
        throw new Error("Manager returned a mismatched profile projection");
      }
    } else if (isTerminalOperation(result.operation) &&
               result.operation.phase === "succeeded") {
      throw new Error("Manager omitted the committed profile projection");
    }
  }

  /** @param {Object} operation @param {Object} plan */
  function validateOperation(operation, plan) {
    if (!operation ||
        operation.schema !== OPERATION_SCHEMA ||
        operation.id !== plan.operationId ||
        operation.planDigest !== plan.planDigest ||
        operation.baseRevision !== plan.baseRevision ||
        !operation.owner ||
        operation.owner.kind !== "profile" ||
        operation.owner.id !== plan.profile ||
        !operation.phase ||
        !Array.isArray(operation.effects) ||
        !operation.recovery) {
      throw new Error("Manager returned a mismatched durable operation");
    }
  }

  /** @param {Object} operation */
  function isTerminalOperation(operation) {
    return Boolean(operation && TERMINAL_PHASES.has(operation.phase));
  }

  /** @param {Object} transaction */
  function returnToEdit(transaction) {
    const next = clone(transaction);
    next.stage = STAGE_EDITING;
    next.stageBeforeStale = "";
    next.reviewedDraftFingerprint = "";
    next.plan = null;
    next.operation = null;
    next.resultProjection = null;
    next.error = "";
    next.authorityReason = "";
    next.responseLost = false;
    return next;
  }

  /** @param {Object} plan @param {Date=} now */
  function planView(plan, now) {
    if (!plan) return null;
    return {
      operationId: plan.operationId || "",
      planDigest: plan.planDigest || "",
      profile: plan.profile || "",
      baseRevision: plan.baseRevision || 0,
      expiresAt: plan.expiresAt || "",
      expired: !isBefore(now || new Date(), plan.expiresAt),
      changes: (plan.canonicalChanges || []).map((change) => ({
        kind: change.kind,
        value: clone(change.value)
      })),
      diff: (plan.diff || []).map((entry) => ({
        kind: entry.kind,
        field: entry.field,
        before: entry.before,
        after: entry.after,
        scope: entry.scope
      })),
      effects: (plan.effects || []).map((effect) => ({
        id: effect.effectId,
        kind: effect.kind,
        scope: effect.scope,
        provider: effect.provider,
        live: Boolean(effect.live),
        summary: effect.summary,
        proofRequired: [...(effect.proofRequired || [])]
      })),
      blockers: (plan.blockers || []).map((blocker) => ({
        code: blocker.code,
        resource: blocker.resource || "",
        owner: blocker.owner || "",
        phase: blocker.phase || "",
        summary: blocker.summary,
        recovery: blocker.recovery
      })),
      warnings: (plan.warnings || []).map((warning) => ({
        code: warning.code,
        summary: warning.summary
      })),
      rollback: {
        mode: plan.rollback && plan.rollback.mode || "unavailable",
        summary: plan.rollback && plan.rollback.summary || "Unavailable",
        effects: [...(plan.rollback && plan.rollback.effects || [])]
      },
      highRisk: highRisk(plan)
    };
  }

  /** @param {Object} transaction */
  function terminalView(transaction) {
    const operation = transaction && transaction.operation;
    if (!operation) {
      return {
        operationId: transaction && transaction.plan &&
          transaction.plan.operationId || "",
        phase: "outcome-unknown",
        terminal: false,
        responseLost: true,
        effects: [],
        result: null,
        recovery: null,
        error: transaction && transaction.error || ""
      };
    }
    return {
      operationId: operation.id,
      phase: operation.phase,
      terminal: isTerminalOperation(operation),
      responseLost: Boolean(transaction.responseLost),
      effects: (operation.effects || []).map((effect) => ({
        id: effect.id,
        kind: effect.kind,
        provider: effect.provider,
        phase: effect.phase,
        evidence: (effect.evidence || []).map((item) => ({
          code: item.code,
          value: item.value || "",
          observedAt: item.observedAt || ""
        }))
      })),
      result: operation.result ? clone(operation.result) : null,
      recovery: operation.recovery ? clone(operation.recovery) : null,
      error: transaction.error || ""
    };
  }

  /** @param {Object} current */
  function desiredSummary(current) {
    const desired = current && current.desired || {};
    const network = desired.network || {};
    const environment = desired.env || {};
    const hostfs = desired.hostfs || {};
    const proxies = desired.commandProxy && desired.commandProxy.commands || {};
    const adapters = desired.commandAdapters &&
      desired.commandAdapters.adapters || {};
    const enabledAdapters = Object.values(adapters).filter(
      (adapter) => adapter && adapter.enabled
    ).length;
    const retention = desired.activity && desired.activity.retention || null;
    return {
      network: {
        mode: network.mode === "tun2socks" ? "proxy" :
          network.mode || "direct",
        proxySecretRef: network.proxySecretRef || "not configured",
        dns: network.mediatedResolver ?
          `mediated via ${network.mediatedResolver}` : "system"
      },
      environment:
        `${Object.keys(environment.public || {}).length} set · ` +
        `${(environment.inherit || []).length} inherit · ` +
        `${(environment.deny || []).length} deny`,
      hostfs:
        `${(hostfs.grants || []).length} allow · ` +
        `${(hostfs.deny || []).length} deny`,
      commandProxies: Object.keys(proxies).sort(),
      commandAdapters:
        `${Object.keys(adapters).length} configured · ` +
        `${enabledAdapters} enabled`,
      retention: retention ?
        `${retention.maxBytes} bytes / ` +
          (retention.maxAgeSeconds ?
            `${retention.maxAgeSeconds} seconds` : "owner lifecycle") :
        "256 MiB / owner lifecycle (default)"
    };
  }

  /** @param {Object} current */
  function effectiveSummary(current) {
    const effective = current && current.effective || {};
    const network = effective.network || null;
    const sessions = effective.sessions || [];
    return {
      status: effective.status || "not-observed",
      network: network ? {
        mode: network.mode === "tun2socks" ? "proxy" :
          network.mode || "unknown",
        proxySecretRef: network.proxySecretRef || "not configured",
        secretGeneration: network.secretGeneration || 0,
        dns: network.dns || "system",
        observedAt: network.observedAt || ""
      } : null,
      currentSessions: sessions.filter((session) => session.current).length,
      olderSessions: sessions.filter((session) => !session.current).length,
      sessions: clone(sessions)
    };
  }

  /** @param {Object} current */
  function transitionSummary(current) {
    const transition = current && current.transition;
    if (!transition) {
      return {
        active: false,
        phase: "none",
        kind: "",
        operationId: "",
        blockers: [],
        startedAt: ""
      };
    }
    return {
      active: true,
      phase: transition.phase || "unknown",
      kind: transition.kind || "",
      operationId: transition.operationId || "",
      blockers: [...(transition.blockers || [])],
      startedAt: transition.startedAt || ""
    };
  }

  /**
   * @param {Object} snapshot
   * @param {string} profile
   */
  function fieldRows(snapshot, profile) {
    const current = projection(snapshot, profile);
    if (!current) return [];
    const desired = desiredSummary(current);
    const effective = effectiveSummary(current);
    const transition = transitionSummary(current);
    return DEFINITIONS.flatMap((entry) => {
      const advertised = capability(snapshot, entry.capability);
      if (!advertised) return [];
      let desiredValue = "configured";
      let effectiveValue =
        `${effective.currentSessions} current · ` +
        `${effective.olderSessions} older session snapshots`;
      switch (entry.kind) {
      case "network.posture":
        desiredValue = desired.network.mode;
        effectiveValue = effective.network ?
          effective.network.mode : effective.status;
        break;
      case "network.proxyRef":
        desiredValue = desired.network.proxySecretRef;
        effectiveValue = effective.network ?
          effective.network.proxySecretRef +
            (effective.network.secretGeneration ?
              ` · generation ${effective.network.secretGeneration}` : "") :
          effective.status;
        break;
      case "network.dns":
        desiredValue = desired.network.dns;
        effectiveValue = effective.network ?
          effective.network.dns : effective.status;
        break;
      case "profile.environment":
        desiredValue = desired.environment;
        break;
      case "profile.hostfs":
        desiredValue = desired.hostfs;
        break;
      case "profile.commandProxy":
        desiredValue = desired.commandProxies.length ?
          desired.commandProxies.join(", ") : "none";
        break;
      case "profile.commandAdapter":
        desiredValue = desired.commandAdapters;
        break;
      case "activity.retention":
        desiredValue = desired.retention;
        effectiveValue = "future exact activity owners";
        break;
      }
      return [{
        kind: entry.kind,
        capability: entry.capability,
        label: entry.label,
        scope: entry.scope,
        desired: desiredValue,
        effective: effectiveValue,
        transition: transition.active ?
          `${transition.phase} · ${transition.kind} · ` +
            transition.operationId :
          "none",
        editable:
          advertised.mutable === true &&
          advertised.state === "available",
        reason: advertised.reason || (
          advertised.mutable ? "" : "Manager marked this capability read-only"
        )
      }];
    });
  }

  /** @param {Object} snapshot */
  function rows(snapshot) {
    return (snapshot && snapshot.profiles || []).map((value) => ({
      profile: value.profile,
      revision: value.revision,
      contentDigest: value.contentDigest,
      desired: desiredSummary(value),
      desiredNetwork: value.desired && value.desired.network || {},
      effective: effectiveSummary(value),
      transition: transitionSummary(value),
      fields: fieldRows(snapshot, value.profile)
    }));
  }

  root.Config = Object.freeze({
    DRAFT_SCHEMA,
    PLAN_SCHEMA,
    APPLY_SCHEMA,
    STAGE_EDITING,
    STAGE_PLANNING,
    STAGE_REVIEW,
    STAGE_CONFIRMING,
    STAGE_APPLYING,
    STAGE_TERMINAL,
    STAGE_STALE,
    STAGE_ERROR,
    DEFINITIONS,
    projection,
    definition,
    createDraft,
    withChange,
    withoutChange,
    withMergedChange,
    draftFingerprint,
    canonicalJSON,
    canonicalPlanDigest,
    buildChange,
    formModel,
    createTransaction,
    editTransaction,
    removeTransactionChange,
    sync,
    confirmability,
    startReview,
    finishReview,
    startConfirmation,
    startApply,
    finishApply,
    returnToEdit,
    highRisk,
    planView,
    terminalView,
    desiredSummary,
    effectiveSummary,
    transitionSummary,
    fieldRows,
    rows
  });
})();
