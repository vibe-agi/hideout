// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole = window.HideoutConsole || {};

  const OPERATION_SCHEMA = "hideout.migration-operation-projection/v1";
  const EXPORT_REQUEST_SCHEMA = "hideout.migration-export-request/v1";
  const EXPORT_PLAN_SCHEMA = "hideout.migration-export-plan/v1";
  const EXPORT_APPLY_SCHEMA = "hideout.migration-export-apply/v1";
  const IMPORT_DRAFT_SCHEMA = "hideout.migration-import-draft/v1";
  const IMPORT_PLAN_SCHEMA = "hideout.migration-import-plan/v1";
  const IMPORT_APPLY_SCHEMA = "hideout.migration-import-apply/v1";

  const RISK_OPAQUE_GUEST =
    "migration.content.opaque_guest_disk_sensitive";
  const RISK_EXACT_IDENTITY =
    "migration.identity.exact_guest_restore_collision";
  const RISK_SELECTED_SECRETS =
    "migration.secret.selected_value_transfer";

  const OPERATION_PATTERN = /^op_[A-Za-z0-9_-]{8,124}$/;
  const OPAQUE_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{7,127}$/;
  const BUNDLE_PATTERN = /^migb_[A-Za-z0-9_-]{8,123}$/;
  const DIGEST_PATTERN = /^sha256:[a-f0-9]{64}$/;
  const NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
  const SECRET_PATTERN = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;
  const CODE_PATTERN = /^[a-z][a-z0-9._-]{0,127}$/;
  const CONTROL_PATTERN =
    /[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/u;
  const TERMINAL = new Set(["complete", "cancelled", "rolled-back", "failed"]);
  const PHASES = new Set([
    "draft", "validating", "awaiting-confirmation", "claiming",
    "snapshotting", "writing", "sealing", "materializing",
    "preparing-secrets", "adopting", "verifying", "committing",
    "cancelling", "rolling-back", "recoverable-failure",
    "complete", "cancelled", "rolled-back", "failed"
  ]);
  const ACTIONS = new Set([
    "resume", "finish", "rollback", "remove-partial", "manual"
  ]);

  /** @param {unknown} value */
  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  /** @param {unknown} value @param {number} maximum */
  function cleanText(value, maximum = 512) {
    const text = String(value === undefined || value === null ? "" : value);
    if (text.length > maximum || CONTROL_PATTERN.test(text)) return "";
    return text.trim();
  }

  /** @param {unknown} value */
  function nonnegativeInteger(value) {
    return Number.isSafeInteger(value) && Number(value) >= 0;
  }

  /** @param {unknown} value */
  function validOperation(value) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    const operation = /** @type {Record<string, any>} */ (value);
    const progress = operation.progress;
    const recovery = operation.recovery;
    if (operation.schema !== OPERATION_SCHEMA ||
        !OPERATION_PATTERN.test(operation.operationId || "") ||
        !nonnegativeInteger(operation.revision) || operation.revision < 1 ||
        !BUNDLE_PATTERN.test(operation.bundleId || "") ||
        !["export", "import"].includes(operation.kind) ||
        !PHASES.has(operation.state) ||
        !cleanText(operation.phaseLabel) ||
        !progress || typeof progress !== "object" ||
        !recovery || typeof recovery !== "object" ||
        !Array.isArray(recovery.allowedActions) ||
        !Array.isArray(operation.warnings) ||
        !Array.isArray(operation.effects)) {
      return false;
    }
    for (const name of [
      "completedLogicalBytes", "completedEncodedBytes",
      "componentsComplete", "elapsedSeconds"
    ]) {
      if (!nonnegativeInteger(progress[name])) return false;
    }
    for (const name of [
      "totalLogicalBytes", "totalEncodedBytes", "componentsTotal",
      "remainingSeconds", "retainedBytes"
    ]) {
      if (progress[name] !== undefined && !nonnegativeInteger(progress[name])) {
        return false;
      }
    }
    if (typeof progress.logicalTotalKnown !== "boolean" ||
        typeof progress.encodedTotalKnown !== "boolean" ||
        typeof progress.remainingKnown !== "boolean" ||
        typeof progress.cancelPending !== "boolean") return false;
    if (progress.logicalTotalKnown &&
        (!nonnegativeInteger(progress.totalLogicalBytes) ||
         progress.completedLogicalBytes > progress.totalLogicalBytes)) return false;
    if (!progress.logicalTotalKnown && (progress.totalLogicalBytes || 0) !== 0) return false;
    if (progress.encodedTotalKnown &&
        (!nonnegativeInteger(progress.totalEncodedBytes) ||
         progress.completedEncodedBytes > progress.totalEncodedBytes)) return false;
    if (!progress.encodedTotalKnown && (progress.totalEncodedBytes || 0) !== 0) return false;
    if (progress.remainingKnown === false && (progress.remainingSeconds || 0) !== 0) {
      return false;
    }
    if (recovery.required) {
      if (recovery.allowedActions.length !== 1 ||
          !ACTIONS.has(recovery.allowedActions[0]) ||
          !CODE_PATTERN.test(recovery.code || "") ||
          !cleanText(recovery.nextAction)) return false;
    } else if (recovery.allowedActions.length !== 0 ||
               !CODE_PATTERN.test(recovery.code || "") ||
               cleanText(recovery.nextAction)) {
      return false;
    }
    if (operation.terminalReceipt && !TERMINAL.has(operation.state)) return false;
    return true;
  }

  /** @param {Object} snapshot */
  function operations(snapshot) {
    return (snapshot && snapshot.migrations || [])
      .filter(validOperation)
      .map(clone)
      .sort((left, right) => {
        const leftAt = Date.parse(
          left.progress.checkpointAt || left.progress.phaseStartedAt || 0
        ) || 0;
        const rightAt = Date.parse(
          right.progress.checkpointAt || right.progress.phaseStartedAt || 0
        ) || 0;
        if (leftAt !== rightAt) return rightAt - leftAt;
        return left.operationId.localeCompare(right.operationId);
      });
  }

  /**
   * @param {Array<Object>} current
   * @param {Array<Object>} incoming
   */
  function mergeOperations(current, incoming) {
    const previous = new Map(
      (current || []).filter(validOperation).map((value) => [value.operationId, value])
    );
    return (incoming || []).filter(validOperation).map((value) => {
      const known = previous.get(value.operationId);
      return clone(known && known.revision > value.revision ? known : value);
    });
  }

  /** @param {number} value */
  function bytes(value) {
    if (!nonnegativeInteger(value)) return "unknown";
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let number = Number(value);
    let unit = 0;
    while (number >= 1024 && unit < units.length - 1) {
      number /= 1024;
      unit++;
    }
    return unit === 0 ? `${number} B` : `${number.toFixed(1)} ${units[unit]}`;
  }

  /** @param {number} value */
  function duration(value) {
    if (!nonnegativeInteger(value)) return "unknown";
    if (value < 60) return `${value} seconds`;
    if (value < 3600) return `${Math.floor(value / 60)}m ${value % 60}s`;
    return `${Math.floor(value / 3600)}h ${Math.floor(value % 3600 / 60)}m`;
  }

  /** @param {Object} operation */
  function nextAction(operation) {
    if (operation.recovery && operation.recovery.required) {
      return operation.recovery.nextAction;
    }
    if (operation.terminalReceipt) return "Review the terminal receipt.";
    const values = {
      draft: "Review the migration plan.",
      validating: "Wait for source and destination checks.",
      "awaiting-confirmation": "Review and confirm the exact plan.",
      claiming: "Wait while Hideout reserves exact resources.",
      snapshotting: "Keep source environments stopped while the snapshot completes.",
      writing: "Keep the daemon running while the encrypted bundle is written.",
      sealing: "Wait for final bundle authentication.",
      materializing: "Keep the destination available while persistent data is copied.",
      "preparing-secrets": "Wait while selected destination secrets are prepared.",
      adopting: "Wait while the chosen identity policy is applied.",
      verifying: "Wait for destination verification.",
      committing: "Wait for atomic destination publication.",
      cancelling: "Wait for the next safe cancellation boundary.",
      "rolling-back": "Wait while staged destination data is removed."
    };
    return values[operation.state] || "Refresh current migration status.";
  }

  /** @param {Object} operation */
  function operationView(operation) {
    if (!validOperation(operation)) throw new Error("migration operation is invalid");
    const progress = operation.progress;
    return {
      id: operation.operationId,
      revision: operation.revision,
      bundle: operation.bundleId,
      kind: operation.kind,
      state: operation.state,
      phase: operation.phaseLabel,
      currentItem: cleanText(progress.currentItem) || "No current item reported",
      logical:
        `${bytes(progress.completedLogicalBytes)} / ` +
        (progress.logicalTotalKnown ? bytes(progress.totalLogicalBytes) : "total unknown"),
      encoded:
        `${bytes(progress.completedEncodedBytes)} / ` +
        (progress.encodedTotalKnown ? bytes(progress.totalEncodedBytes) : "total unknown"),
      components: progress.componentsTotal ?
        `${progress.componentsComplete} / ${progress.componentsTotal}` :
        `${progress.componentsComplete} / total unknown`,
      elapsed: duration(progress.elapsedSeconds),
      eta: progress.remainingKnown ? duration(progress.remainingSeconds) : "unknown",
      retained: bytes(progress.retainedBytes || 0),
      cancelPending: Boolean(progress.cancelPending),
      blockers: [
        ...(operation.warnings || []).map(
          (value) => `${cleanText(value.code)}: ${cleanText(value.summary, 2048)}`
        ),
        ...(operation.lastErrorCode ? [operation.lastErrorCode] : [])
      ].filter(Boolean),
      next: nextAction(operation),
      recovery: clone(operation.recovery),
      effects: clone(operation.effects || []),
      identityPolicies: clone(operation.identityPolicies || {}),
      receipt: operation.terminalReceipt ? clone(operation.terminalReceipt) : null,
      terminal: TERMINAL.has(operation.state)
    };
  }

  /** @param {unknown} raw */
  function commaList(raw) {
    return [...new Set(String(raw || "").split(",")
      .map((value) => value.trim()).filter(Boolean))].sort();
  }

  /** @param {Object} input */
  function buildExportRequest(input) {
    const mode = String(input.mode || "config");
    const names = [...new Set((input.environmentNames || []).map(String))].sort();
    const secrets = commaList(input.includeSecretRefs || []);
    const outputPath = cleanText(input.outputPath, 4096);
    if (!["config", "full"].includes(mode) || !names.length ||
        names.some((name) => !NAME_PATTERN.test(name)) ||
        secrets.some((name) => !SECRET_PATTERN.test(name)) ||
        !outputPath.startsWith("/") || outputPath.includes("\0")) {
      throw new Error("migration export choices are incomplete or invalid");
    }
    const risks = [];
    if (mode === "full") risks.push(RISK_OPAQUE_GUEST);
    if (secrets.length) risks.push(RISK_SELECTED_SECRETS);
    return {
      schema: EXPORT_REQUEST_SCHEMA,
      mode,
      environmentNames: names,
      includeSecretRefs: secrets,
      outputPath,
      riskAcknowledgements: risks.sort()
    };
  }

  /** @param {Object} inspection */
  function importChoices(inspection) {
    const inventory = inspection && inspection.inventory;
    if (!inventory || inventory.schema !== "hideout.migration-bundle-inspection/v1" ||
        !BUNDLE_PATTERN.test(inventory.bundleId || "") ||
        inventory.sealed !== true || !Array.isArray(inventory.environments) ||
        !inventory.environments.length) {
      throw new Error("authenticated migration inventory is invalid");
    }
    return {
      environments: inventory.environments.map((value) => ({
        sourceRef: value.sourceRef,
        label: cleanText(value.displayNameHint) || value.sourceRef,
        selected: false,
        destinationName: cleanText(value.displayNameHint) || "imported",
        policy: "safe-clone"
      })),
      workspaces: inventory.environments.flatMap((environment) =>
        (environment.workspaceProposals || []).map((proposal) => ({
          environmentRef: environment.sourceRef,
          proposalId: proposal.proposalId,
          guestPath: cleanText(proposal.guestPath, 4096),
          hint: cleanText(proposal.hostPathHint, 4096),
          decision: "disabled",
          destinationPath: ""
        }))
      ),
      secrets: (inventory.secrets || []).map((value) => ({
        sourceRef: value.secretRef,
        label: cleanText(value.displayName) || value.secretRef,
        valueIncluded: Boolean(value.valueIncluded),
        decision: "unresolved",
        destinationRef: ""
      })),
      authorities: (inventory.authorityProposals || []).map((value) => ({
        proposalId: value.proposalId,
        class: cleanText(value.class),
        summary: cleanText(value.sourceSummary, 4096),
        decision: "disabled",
        destinationValue: ""
      }))
    };
  }

  /**
   * @param {Object} inspection
   * @param {string} bundlePath
   * @param {Object} choices
   */
  function buildImportDraft(inspection, bundlePath, choices) {
    const selected = (choices.environments || [])
      .filter((value) => value.selected)
      .sort((left, right) => left.sourceRef.localeCompare(right.sourceRef));
    if (!selected.length || !cleanText(bundlePath, 4096).startsWith("/")) {
      throw new Error("select at least one environment and an absolute bundle path");
    }
    for (const value of selected) {
      if (!OPAQUE_PATTERN.test(value.sourceRef || "") ||
          !NAME_PATTERN.test(value.destinationName || "") ||
          !["safe-clone", "exact-guest-restore"].includes(value.policy)) {
        throw new Error("an environment destination decision is invalid");
      }
    }
    const selectedRefs = new Set(selected.map((value) => value.sourceRef));
    const workspaceMappings = (choices.workspaces || [])
      .filter((value) => selectedRefs.has(value.environmentRef))
      .map((value) => ({
        proposalId: value.proposalId,
        decision: value.decision,
        ...(value.decision === "mapped" ? {
          destinationPath: cleanText(value.destinationPath, 4096)
        } : {})
      }))
      .sort((left, right) => left.proposalId.localeCompare(right.proposalId));
    if (workspaceMappings.some((value) =>
      !["disabled", "mapped"].includes(value.decision) ||
      (value.decision === "mapped" && !value.destinationPath.startsWith("/")))) {
      throw new Error("a workspace destination decision is invalid");
    }
    const secretMappings = (choices.secrets || []).map((value) => ({
      sourceRef: value.sourceRef,
      decision: value.decision,
      ...(["existing-ref", "import-value"].includes(value.decision) ? {
        destinationRef: cleanText(value.destinationRef, 64)
      } : {})
    })).sort((left, right) => left.sourceRef.localeCompare(right.sourceRef));
    if (secretMappings.some((value) =>
      !["unresolved", "existing-ref", "import-value"].includes(value.decision) ||
      (value.decision !== "unresolved" && !SECRET_PATTERN.test(value.destinationRef || "")))) {
      throw new Error("a secret destination decision is invalid");
    }
    const authorityMappings = (choices.authorities || []).map((value) => ({
      proposalId: value.proposalId,
      decision: value.decision,
      ...(value.decision === "approved" ? {
        destinationValue: cleanText(value.destinationValue, 4096)
      } : {})
    })).sort((left, right) => left.proposalId.localeCompare(right.proposalId));
    for (const value of authorityMappings) {
      if (!["disabled", "approved"].includes(value.decision)) {
        throw new Error("an authority destination decision is invalid");
      }
      if (value.decision === "approved") {
        try {
          JSON.parse(value.destinationValue);
        } catch {
          throw new Error("approved destination authority must be valid JSON");
        }
      }
    }
    const risks = [];
    if (selected.some((value) => value.policy === "exact-guest-restore")) {
      risks.push(RISK_EXACT_IDENTITY);
    }
    if (secretMappings.some((value) => value.decision === "import-value")) {
      risks.push(RISK_SELECTED_SECRETS);
    }
    return {
      schema: IMPORT_DRAFT_SCHEMA,
      bundlePath: cleanText(bundlePath, 4096),
      bundleBinding: clone(inspection.binding),
      selectedEnvironmentRefs: selected.map((value) => value.sourceRef),
      nameMappings: selected.map((value) => ({
        sourceRef: value.sourceRef,
        destinationName: value.destinationName
      })),
      conflictDecisions: [],
      workspaceMappings,
      secretMappings,
      identityPolicies: selected.map((value) => ({
        sourceRef: value.sourceRef,
        policy: value.policy
      })),
      authorityDecisions: authorityMappings,
      riskAcknowledgements: risks.sort()
    };
  }

  /** @param {Object} plan @param {string} schema */
  function validatePlan(plan, schema) {
    if (!plan || plan.schema !== schema ||
        !OPAQUE_PATTERN.test(plan.planId || "") ||
        !DIGEST_PATTERN.test(plan.planDigest || "") ||
        !Array.isArray(plan.effects) || !Array.isArray(plan.riskAcknowledgements || [])) {
      throw new Error("migration plan response is invalid");
    }
    return plan;
  }

  /** @param {Object} plan */
  function exportPlanView(plan) {
    validatePlan(plan, EXPORT_PLAN_SCHEMA);
    if (!Array.isArray(plan.environmentRefs) ||
        !Array.isArray(plan.diskRefs) ||
        !Array.isArray(plan.selectedSecretRefs) ||
        !Array.isArray(plan.includedClasses) ||
        !Array.isArray(plan.excludedClasses) ||
        !Array.isArray(plan.environmentEstimates) ||
        !Array.isArray(plan.diskEstimates) ||
        plan.environmentEstimates.length !== plan.environmentRefs.length ||
        plan.diskEstimates.length !== plan.diskRefs.length ||
        !Number.isSafeInteger(plan.estimatedPayloadLogicalBytes) ||
        plan.estimatedPayloadLogicalBytes <= 0 ||
        typeof plan.estimatedPayloadComplete !== "boolean") {
      throw new Error("migration export inventory response is invalid");
    }
    plan.environmentEstimates.forEach((value, index) => {
      if (!value || value.environmentRef !== plan.environmentRefs[index] ||
          typeof value.displayName !== "string" || !value.displayName ||
          !Number.isSafeInteger(value.portableConfigLogicalBytes) ||
          value.portableConfigLogicalBytes <= 0 ||
          !DIGEST_PATTERN.test(value.portableConfigDigest || "") ||
          !Array.isArray(value.diskRefs) ||
          !Number.isSafeInteger(value.referencedDiskLogicalBytes) ||
          !Number.isSafeInteger(value.estimatedLogicalBytes) ||
          value.estimatedLogicalBytes <= 0) {
        throw new Error("migration export environment estimate is invalid");
      }
    });
    plan.diskEstimates.forEach((value, index) => {
      if (!value || value.diskRef !== plan.diskRefs[index] ||
          !["root", "attached"].includes(value.role) ||
          !Number.isSafeInteger(value.logicalBytes) || value.logicalBytes <= 0 ||
          !Number.isSafeInteger(value.allocatedBytesHint) ||
          !Array.isArray(value.consumers) || !value.consumers.length) {
        throw new Error("migration export disk estimate is invalid");
      }
    });
    return {
      kind: "export", id: plan.planId, digest: plan.planDigest,
      mode: plan.mode, outputPath: plan.outputPath,
      environments: plan.environmentRefs || [], disks: plan.diskRefs || [],
      secrets: plan.selectedSecretRefs || [], exclusions: plan.excludedClasses || [],
      included: plan.includedClasses,
      environmentEstimates: clone(plan.environmentEstimates),
      diskEstimates: clone(plan.diskEstimates),
      payloadEstimate: `${bytes(plan.estimatedPayloadLogicalBytes)}${
        plan.estimatedPayloadComplete ? " (complete logical payload)" :
          " minimum (selected secret value sizes hidden)"
      }`,
      risks: plan.riskAcknowledgements || [], warnings: plan.warnings || [],
      effects: plan.effects || [], blockers: [],
      compatibility: null,
      confirmation: plan.confirmationText || "Review and confirm this exact plan."
    };
  }

  /** @param {Object} plan */
  function importPlanView(plan) {
    validatePlan(plan, IMPORT_PLAN_SCHEMA);
    if (!plan.compatibility || !Array.isArray(plan.objects) ||
        !Array.isArray(plan.blockers)) throw new Error("import plan response is invalid");
    return {
      kind: "import", id: plan.planId, digest: plan.planDigest,
      environments: plan.objects || [], identities: plan.identityActions || [],
      workspaces: plan.workspaceActions || [], secrets: plan.secretActions || [],
      authorities: plan.authorityActions || [], disabled: plan.disabledProposals || [],
      conflicts: plan.conflictActions || [], risks: plan.riskAcknowledgements || [],
      effects: plan.effects || [], blockers: plan.blockers || [],
      compatibility: clone(plan.compatibility),
      confirmation: "Review compatibility, mappings, effects, and blockers."
    };
  }

  function idempotencyKey() {
    if (!window.crypto || !window.crypto.getRandomValues) {
      throw new Error("secure browser randomness is unavailable");
    }
    const bytes = new Uint8Array(16);
    window.crypto.getRandomValues(bytes);
    return "webui_" + Array.from(bytes)
      .map((value) => value.toString(16).padStart(2, "0")).join("");
  }

  /** @param {Object} plan @param {string} handle */
  function exportApply(plan, handle) {
    validatePlan(plan, EXPORT_PLAN_SCHEMA);
    if (!cleanText(handle, 128)) throw new Error("protected export input expired");
    return {
      schema: EXPORT_APPLY_SCHEMA,
      plan: clone(plan),
      confirmation: {
        planDigest: plan.planDigest,
        acceptedRiskAcknowledgements: clone(plan.riskAcknowledgements || []),
        approvedAuthorityProposalIds: []
      },
      secretInputHandle: handle,
      idempotencyKey: idempotencyKey()
    };
  }

  /** @param {Object} plan @param {string} handle */
  function importApply(plan, handle) {
    const view = importPlanView(plan);
    if (!cleanText(handle, 128) || view.blockers.length ||
        !view.compatibility.available) {
      throw new Error("import plan is blocked or its protected input expired");
    }
    return {
      schema: IMPORT_APPLY_SCHEMA,
      plan: clone(plan),
      confirmation: {
        planDigest: plan.planDigest,
        acceptedRiskAcknowledgements: clone(plan.riskAcknowledgements || []),
        approvedAuthorityProposalIds: (plan.authorityActions || [])
          .filter((value) => value.approved)
          .map((value) => value.proposalId).sort()
      },
      secretInputHandle: handle,
      idempotencyKey: idempotencyKey()
    };
  }

  root.Migration = Object.freeze({
    OPERATION_SCHEMA,
    EXPORT_REQUEST_SCHEMA,
    EXPORT_PLAN_SCHEMA,
    IMPORT_DRAFT_SCHEMA,
    IMPORT_PLAN_SCHEMA,
    RISK_OPAQUE_GUEST,
    RISK_EXACT_IDENTITY,
    RISK_SELECTED_SECRETS,
    validOperation,
    operations,
    mergeOperations,
    bytes,
    duration,
    nextAction,
    operationView,
    buildExportRequest,
    importChoices,
    buildImportDraft,
    exportPlanView,
    importPlanView,
    exportApply,
    importApply
  });
})();
