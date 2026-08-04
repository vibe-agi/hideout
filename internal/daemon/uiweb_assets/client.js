// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole = window.HideoutConsole || {};
  let token = "";
  let credentialEpoch = 0;
  let rejectedEpoch = -1;
  const credentialListeners = new Set();
  const authorityListeners = new Set();
  const requests = new Set();

  function clearFragment() {
    if (!window.history || !window.history.replaceState) return;
    window.history.replaceState(
      null,
      document.title,
      window.location.pathname + window.location.search
    );
  }

  /**
   * A newly issued credential enters only through the URL fragment. Fragments
   * are not sent to the loopback server, and the value is removed from browser
   * history before any Manager request starts.
   *
   * @returns {boolean}
   */
  function refreshCredentialFromLocation() {
    const params = new URLSearchParams(window.location.hash.slice(1));
    const candidate = params.get("token") || "";
    if (candidate) clearFragment();
    if (!/^ui_[0-9a-f]{48}$/.test(candidate) || candidate === token) {
      return false;
    }
    for (const controller of requests) controller.abort();
    requests.clear();
    token = candidate;
    credentialEpoch++;
    rejectedEpoch = -1;
    for (const listener of credentialListeners) {
      try {
        listener({available: true, epoch: credentialEpoch});
      } catch {
        // A presentation listener cannot block credential replacement.
      }
    }
    return true;
  }

  refreshCredentialFromLocation();
  if (window.addEventListener) {
    window.addEventListener("hashchange", refreshCredentialFromLocation);
  }

  /** @param {string} reason */
  function notifyAuthorityLost(reason) {
    if (rejectedEpoch === credentialEpoch) return;
    rejectedEpoch = credentialEpoch;
    for (const controller of requests) controller.abort();
    requests.clear();
    for (const listener of authorityListeners) {
      try {
        listener({
          state: "credential-expired",
          reason: reason || "operator credential was rejected",
          epoch: credentialEpoch
        });
      } catch {
        // Authority loss remains fail-closed even if presentation fails.
      }
    }
  }

  /** @param {string} message @param {string} code */
  function clientError(message, code) {
    const error = new Error(message);
    error.code = code;
    error.credentialExpired =
      code === "credential-missing" ||
      code === "credential-expired";
    return error;
  }

  /** @param {string} path @param {RequestInit=} options */
  async function request(path, options) {
    if (!token) {
      const error = clientError(
        "operator credential is missing; open a freshly issued WebUI link",
        "credential-missing"
      );
      notifyAuthorityLost(error.message);
      throw error;
    }
    const requestEpoch = credentialEpoch;
    const requestToken = token;
    const init = Object.assign({}, options || {});
    const headers = new Headers(init.headers || {});
    headers.set("X-Hideout-UI-Token", requestToken);
    init.headers = headers;
    init.credentials = "omit";
    init.cache = "no-store";
    const controller = new AbortController();
    init.signal = controller.signal;
    requests.add(controller);
    let response;
    try {
      response = await fetch(path, init);
    } catch (cause) {
      requests.delete(controller);
      if (requestEpoch !== credentialEpoch) {
        throw clientError(
          "request was superseded by a fresh operator credential",
          "credential-refreshed"
        );
      }
      const error = new Error(`Hideout request did not return: ${String(cause)}`);
      error.transport = true;
      error.cause = cause;
      throw error;
    }
    requests.delete(controller);
    if (requestEpoch !== credentialEpoch) {
      throw clientError(
        "response belongs to a superseded operator credential",
        "credential-refreshed"
      );
    }
    const text = await response.text();
    let envelope;
    try {
      envelope = JSON.parse(text);
    } catch {
      if (response.status === 401) {
        const error = clientError(
          "operator credential expired or was rejected; open a freshly issued WebUI link",
          "credential-expired"
        );
        error.status = response.status;
        notifyAuthorityLost(error.message);
        throw error;
      }
      const error = new Error(
        `Hideout returned invalid JSON (${response.status})`
      );
      error.transport = true;
      error.status = response.status;
      throw error;
    }
    if (!response.ok) {
      const detail = envelope.errorDetails && envelope.errorDetails[0];
      const expired = response.status === 401;
      const error = clientError(
        (detail && `${detail.code}: ${detail.message}`) ||
        (envelope.errors || []).join("; ") ||
        response.statusText ||
        (expired ? "operator credential was rejected" : "Hideout request failed"),
        expired ? "credential-expired" : detail && detail.code || ""
      );
      error.transport = false;
      error.status = response.status;
      error.recovery = detail && detail.recovery || "";
      if (expired) notifyAuthorityLost(error.message);
      throw error;
    }
    return envelope;
  }

  /**
   * @param {string} resource
   * @param {Object} payload
   */
  async function post(resource, payload) {
    const allowed = new Set([
      "profile/transaction/plan",
      "profile/transaction/apply"
    ]);
    if (!allowed.has(resource)) {
      throw new Error("unsupported browser mutation resource");
    }
    const envelope = await request(`/api/v1/${resource}`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== resource ||
        !envelope.data) {
      throw new Error(`${resource} response contract mismatch`);
    }
    return envelope.data;
  }

  async function snapshot() {
    const envelope = await request(
      "/api/v1/operator/snapshot?activityLimit=100"
    );
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== "operator/snapshot" ||
        !envelope.data) {
      throw new Error("operator snapshot response contract mismatch");
    }
    return envelope.data;
  }

  /** @param {Record<string, unknown>} values */
  function queryString(values) {
    const query = new URLSearchParams();
    for (const [name, raw] of Object.entries(values || {})) {
      if (raw === undefined || raw === null || raw === "") continue;
      const entries = Array.isArray(raw) ? raw : [raw];
      for (const entry of entries) {
        if (entry === undefined || entry === null || entry === "") continue;
        query.append(name, String(entry));
      }
    }
    return query.toString();
  }

  /**
   * @param {"summary"|"events"|"executions"|"coverage"|"risks"} resource
   * @param {Record<string, unknown>} query
   */
  async function activity(resource, query) {
    const allowed = new Set([
      "summary", "events", "executions", "coverage", "risks"
    ]);
    if (!allowed.has(resource)) throw new Error("unsupported activity resource");
    const encoded = queryString(query);
    const envelope = await request(
      `/api/v1/activity/${resource}${encoded ? `?${encoded}` : ""}`
    );
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== `activity/${resource}` ||
        !envelope.data) {
      throw new Error(`activity ${resource} response contract mismatch`);
    }
    return envelope.data;
  }

  /** @param {Object} draft */
  function configurationPlan(draft) {
    return post("profile/transaction/plan", draft);
  }

  /** @param {Object} applyRequest */
  function configurationApply(applyRequest) {
    return post("profile/transaction/apply", applyRequest);
  }

  /** @param {string} resource @param {Object} payload */
  async function migrationPost(resource, payload) {
    const allowed = new Set([
      "migration/secret-input",
      "migration/export/plan",
      "migration/export/apply",
      "migration/import/inspect",
      "migration/import/plan",
      "migration/import/apply"
    ]);
    if (!allowed.has(resource)) {
      throw new Error("unsupported browser migration resource");
    }
    const envelope = await request(`/api/v1/${resource}`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== resource ||
        !envelope.data) {
      throw new Error(`${resource} response contract mismatch`);
    }
    return envelope.data;
  }

  /** @param {Object} payload */
  function migrationSecretInput(payload) {
    return migrationPost("migration/secret-input", payload);
  }

  /** @param {Object} payload */
  function migrationExportPlan(payload) {
    return migrationPost("migration/export/plan", payload);
  }

  /** @param {Object} payload */
  function migrationExportApply(payload) {
    return migrationPost("migration/export/apply", payload);
  }

  /** @param {Object} payload */
  function migrationImportInspect(payload) {
    return migrationPost("migration/import/inspect", payload);
  }

  /** @param {Object} payload */
  function migrationImportPlan(payload) {
    return migrationPost("migration/import/plan", payload);
  }

  /** @param {Object} payload */
  function migrationImportApply(payload) {
    return migrationPost("migration/import/apply", payload);
  }

  /** @param {string} operationID */
  async function migrationOperation(operationID) {
    if (!/^op_[A-Za-z0-9_-]{8,124}$/.test(operationID)) {
      throw new Error("migration operation identity is invalid");
    }
    const envelope = await request(
      `/api/v1/migration/operations/${encodeURIComponent(operationID)}`
    );
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== "migration/operation" ||
        !envelope.data) {
      throw new Error("migration operation response contract mismatch");
    }
    return envelope.data;
  }

  /**
   * @param {string} operationID
   * @param {"resume"|"cancel"|"recover"} action
   * @param {Object} payload
   */
  async function migrationAction(operationID, action, payload) {
    if (!/^op_[A-Za-z0-9_-]{8,124}$/.test(operationID) ||
        !["resume", "cancel", "recover"].includes(action)) {
      throw new Error("migration operation action is invalid");
    }
    const resource = `migration/operations/${operationID}/${action}`;
    const envelope = await request(`/api/v1/${resource}`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== "migration/operation" ||
        !envelope.data) {
      throw new Error("migration action response contract mismatch");
    }
    return envelope.data;
  }

  /** @param {string} operationID */
  async function operation(operationID) {
    if (!/^op_[A-Za-z0-9_-]{8,124}$/.test(operationID)) {
      throw new Error("operation identity is invalid");
    }
    const envelope = await request(
      `/api/v1/operations/${encodeURIComponent(operationID)}`
    );
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== "operation/inspect" ||
        !envelope.data) {
      throw new Error("operation inspection response contract mismatch");
    }
    return envelope.data;
  }

  /** @param {string} profile */
  async function profileProjection(profile) {
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(profile)) {
      throw new Error("profile name is invalid");
    }
    const envelope = await request(
      `/api/v1/profiles/${encodeURIComponent(profile)}/projection`
    );
    if (envelope.version !== "hideout.manager-api/v1" ||
        envelope.resource !== "profile/projection" ||
        !envelope.data) {
      throw new Error("profile state response contract mismatch");
    }
    return envelope.data;
  }

  /**
   * @param {{
   *   event:(event:Object)=>void,
   *   error:(reason:string)=>void,
   *   open?:()=>void
   * }} handlers
   */
  function events(handlers) {
    if (!token) throw new Error("operator credential is missing");
    const streamEpoch = credentialEpoch;
    const streamToken = token;
    const source = new EventSource(
      "/daemon/events?token=" + encodeURIComponent(streamToken)
    );
    let closed = false;
    const close = () => {
      if (closed) return;
      closed = true;
      source.close();
    };
    source.onopen = function() {
      if (streamEpoch !== credentialEpoch) {
        close();
        return;
      }
      if (handlers.open) handlers.open();
    };
    source.onmessage = function(message) {
      if (streamEpoch !== credentialEpoch) {
        close();
        return;
      }
      try {
        handlers.event(JSON.parse(message.data));
      } catch (error) {
        handlers.error(`invalid event: ${String(error)}`);
        close();
      }
    };
    source.onerror = function() {
      if (closed) return;
      if (streamEpoch !== credentialEpoch) {
        close();
        return;
      }
      handlers.error("event stream closed");
      close();
    };
    return {close};
  }

  /** @param {(state:Object)=>void} listener */
  function onCredentialRefresh(listener) {
    credentialListeners.add(listener);
    return () => credentialListeners.delete(listener);
  }

  /** @param {(state:Object)=>void} listener */
  function onAuthorityLost(listener) {
    authorityListeners.add(listener);
    return () => authorityListeners.delete(listener);
  }

  root.Client = Object.freeze({
    hasCredential: () => Boolean(token),
    credentialState: () => Object.freeze({
      available: Boolean(token),
      epoch: credentialEpoch
    }),
    refreshCredentialFromLocation,
    onCredentialRefresh,
    onAuthorityLost,
    snapshot,
    activity,
    configurationPlan,
    configurationApply,
    migrationSecretInput,
    migrationExportPlan,
    migrationExportApply,
    migrationImportInspect,
    migrationImportPlan,
    migrationImportApply,
    migrationOperation,
    migrationAction,
    operation,
    profileProjection,
    events
  });
})();
