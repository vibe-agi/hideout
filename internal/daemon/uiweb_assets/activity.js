// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole = window.HideoutConsole || {};

  /** @param {Array<Object>} values @param {string} field */
  function countBy(values, field) {
    const counts = new Map();
    for (const value of values || []) {
      const key = String(value && value[field] || "unknown");
      counts.set(key, (counts.get(key) || 0) + Number(value && value.count || 1));
    }
    return Array.from(counts.entries())
      .map(([kind, count]) => ({kind, count}))
      .sort((left, right) => left.kind.localeCompare(right.kind));
  }

  /** @param {Object} snapshot */
  function summarize(snapshot) {
    return {
      counts: countBy(snapshot.activity || [], "kind"),
      recent: (snapshot.activity || []).slice(0, 100),
      coverage: (snapshot.coverage || []).slice(0, 256),
      risks: (snapshot.risks || []).slice(0, 256),
      cursor: String(snapshot.activityCursor || "")
    };
  }

  /** @param {Object} snapshot @param {string} sessionID */
  function ownerForSession(snapshot, sessionID) {
    if (!sessionID) return null;
    const sources = []
      .concat(snapshot.activity || [])
      .concat(snapshot.coverage || []);
    for (const value of sources) {
      if (value && value.sessionId === sessionID && value.owner) {
        return value.owner;
      }
    }
    const session = (snapshot.sessions || []).find(
      (value) => value && value.id === sessionID
    );
    for (const retention of snapshot.activityRetention || []) {
      const owner = retention && retention.owner;
      if (!owner) continue;
      if (owner.kind === "disposable-session" && owner.sessionId === sessionID) {
        return owner;
      }
      if (owner.kind === "reusable-environment" &&
          session &&
          owner.environmentId === session.environmentId) {
        return owner;
      }
    }
    return null;
  }

  /** @param {Object} snapshot @param {string} sessionID */
  function ownerQuery(snapshot, sessionID) {
    const owner = ownerForSession(snapshot, sessionID);
    if (!owner) return null;
    if (owner.kind === "disposable-session") {
      return {session: owner.sessionId, run: sessionID};
    }
    if (owner.kind === "reusable-environment") {
      return {
        environment: owner.environmentId,
        incarnation: owner.backendIncarnationId,
        run: sessionID
      };
    }
    return null;
  }

  /** @param {unknown} value */
  function timeLabel(value) {
    const timestamp = Date.parse(String(value || ""));
    if (!Number.isFinite(timestamp)) return "time unavailable";
    return new Date(timestamp).toISOString();
  }

  /** @param {Object} record */
  function subjectDetail(record) {
    const subject = record && record.subject || {};
    switch (record && record.kind) {
      case "process":
        return [
          subject.executable || "process",
          (subject.argv || []).join(" "),
          subject.cwd ? `cwd=${subject.cwd}` : ""
        ].filter(Boolean).join(" · ");
      case "file":
        return [
          subject.path || "path unavailable",
          subject.targetPath ? `target=${subject.targetPath}` : "",
          subject.pathClass || "",
          subject.destructive ? "destructive" : ""
        ].filter(Boolean).join(" · ");
      case "connection":
        return [
          subject.domain || subject.targetIp || subject.ip || "endpoint unavailable",
          subject.targetPort || subject.port ? `port=${subject.targetPort || subject.port}` : "",
          subject.protocol || "",
          subject.route || "",
          subject.domainAttribution || ""
        ].filter(Boolean).join(" · ");
      case "dns":
        return [
          subject.query || "query unavailable",
          subject.queryType || "",
          (subject.answers || []).join(", "),
          subject.responseCode || "",
          subject.resolver ? `resolver=${subject.resolver}` : ""
        ].filter(Boolean).join(" · ");
      default:
        return subject.summary || subject.code || "details unavailable";
    }
  }

  /** @param {Object} record */
  function eventView(record) {
    const actor = record.actor || record.mediator || {};
    return {
      id: String(record.id || ""),
      kind: String(record.kind || "unknown"),
      operation: String(record.operation || "observed"),
      title: `${record.kind || "activity"} · ${record.operation || "observed"}`,
      detail: subjectDetail(record),
      firstAt: timeLabel(record.firstAt),
      lastAt: timeLabel(record.lastAt),
      count: Number(record.count || 0),
      bytes: Number(record.bytes || 0),
      executionId: String(actor.executionId || ""),
      pid: Number(actor.pid || 0),
      attribution: String(record.attribution || "unknown"),
      outcome: String(record.outcome && record.outcome.status || "unknown"),
      coverageId: String(record.coverageId || ""),
      truncation: Array.isArray(record.truncation) ? record.truncation.slice() : []
    };
  }

  /** @param {Array<Object>} values */
  function newestFirst(values) {
    return (values || [])
      .map((value, index) => {
        const timestamp = Date.parse(String(
          value && (value.lastAt || value.firstAt) || ""
        ));
        return {
          value,
          index,
          timestamp: Number.isFinite(timestamp) ?
            timestamp : Number.NEGATIVE_INFINITY
        };
      })
      .sort((left, right) =>
        right.timestamp - left.timestamp || left.index - right.index
      )
      .map((entry) => entry.value);
  }

  /**
   * @param {Array<Object>} roots
   * @returns {Array<{node:Object,depth:number}>}
   */
  function flattenExecutions(roots) {
    const rows = [];
    function visit(node, depth) {
      if (!node || rows.length >= 500) return;
      rows.push({node, depth: Math.min(depth, 8)});
      for (const child of node.children || []) visit(child, depth + 1);
    }
    for (const rootNode of roots || []) visit(rootNode, 0);
    return rows;
  }

  /** @param {Object} node */
  function executionView(node) {
    const execution = node && node.execution || {};
    const exit = execution.exit;
    let outcome = "running";
    if (exit) {
      if (exit.code !== undefined && exit.code !== null) outcome = `exit ${exit.code}`;
      else if (exit.signal) outcome = `signal ${exit.signal}`;
      else outcome = exit.unknownReason || "ended";
    }
    return {
      id: String(execution.id || ""),
      parent: String(execution.parentExecutionId || ""),
      title: String(execution.executable || execution.id || "execution"),
      argv: (execution.argv || []).join(" "),
      cwd: String(execution.cwd || ""),
      pid: Number(execution.pid || 0),
      identity: execution.guestIdentity || execution.identity || {},
      startedAt: timeLabel(execution.startedAt),
      outcome,
      limitations: (execution.limitations || []).slice(),
      counts: Object.assign({}, node.activityCounts || {}),
      parentUnavailable: Boolean(node.parentUnavailable)
    };
  }

  /** @param {Object} interval */
  function coverageView(interval) {
    return {
      id: String(interval.id || ""),
      subsystem: String(interval.subsystem || "unknown"),
      state: String(interval.state || "Unavailable"),
      reason: String(interval.reason || "reason unavailable"),
      dropped: Number(interval.droppedEventCount || 0),
      retentionGap: Boolean(interval.retentionGap),
      startedAt: timeLabel(interval.startedAt),
      endedAt: interval.endedAt ? timeLabel(interval.endedAt) : "current",
      evidence: (interval.evidence || []).map((value) =>
        value.value ? `${value.code}=${value.value}` : value.code
      )
    };
  }

  /** @param {Object} finding */
  function riskView(finding) {
    return {
      id: String(finding.id || ""),
      title: String(finding.title || finding.ruleId || "risk"),
      rule: String(finding.ruleId || ""),
      severity: String(finding.severity || "unknown"),
      confidence: String(finding.confidence || "unknown"),
      policyStatus: String(finding.policyStatus || "not-evaluated"),
      policyDisposition: String(finding.policyDisposition || ""),
      explanation: String(finding.explanation || ""),
      nextAction: String(finding.nextAction || ""),
      evidenceRefs: (finding.evidenceRefs || []).slice(),
      count: Number(finding.count || 0),
      firstAt: timeLabel(finding.firstAt),
      lastAt: timeLabel(finding.lastAt)
    };
  }

  /** @param {Object} operation */
  function operationView(operation) {
    return {
      id: String(operation.id || ""),
      title: String(operation.kind || operation.id || "operation"),
      phase: String(operation.phase || "unknown"),
      owner: operation.owner ?
        `${operation.owner.kind || "owner"}:${operation.owner.id || "unknown"}` :
        "owner unavailable",
      effects: (operation.effects || []).map((effect) => ({
        id: effect.id,
        kind: effect.kind,
        provider: effect.provider,
        phase: effect.phase,
        evidence: (effect.evidence || []).map((value) =>
          value.value ? `${value.code}=${value.value}` : value.code
        )
      })),
      result: operation.result || null,
      recovery: operation.recovery || {},
      updatedAt: timeLabel(operation.updatedAt)
    };
  }

  /** @param {Object} snapshot @param {string} sessionID */
  function retentionView(snapshot, sessionID) {
    const owner = ownerForSession(snapshot, sessionID);
    const rows = snapshot.activityRetention || [];
    const row = rows.find((value) => {
      if (!owner || !value || !value.owner) return false;
      return JSON.stringify(value.owner) === JSON.stringify(owner);
    });
    if (!row) return null;
    return {
      earliestAt: row.earliestAt ? timeLabel(row.earliestAt) : "empty",
      latestAt: row.latestAt ? timeLabel(row.latestAt) : "empty",
      usedBytes: Number(row.usedBytes || 0),
      limitBytes: Number(row.limitBytes || 0),
      maxAgeSeconds: Number(row.maxAgeSeconds || 0),
      pruned: Boolean(row.pruned),
      corrupt: Boolean(row.corrupt),
      reasons: (row.reasons || []).slice()
    };
  }

  /** @param {unknown} raw @param {string} name @param {number} maximum */
  function boundedText(raw, name, maximum) {
    const value = String(raw || "").trim();
    if (value.length > maximum || /[\u0000-\u001f\u007f]/.test(value)) {
      throw new Error(`${name} is invalid`);
    }
    return value;
  }

  /**
   * @param {unknown} raw
   * @param {string} name
   * @param {RegExp} pattern
   * @param {number=} maximum
   */
  function boundedList(raw, name, pattern, maximum) {
    const source = boundedText(raw, name, 4096);
    if (!source) return [];
    const values = Array.from(new Set(
      source.split(",").map((value) => value.trim()).filter(Boolean)
    )).sort();
    if (values.length > (maximum || 16) ||
        values.some((value) => !pattern.test(value))) {
      throw new Error(`${name} contains an invalid or duplicate value`);
    }
    return values;
  }

  /** @param {unknown} raw @param {string} name */
  function normalizedTime(raw, name) {
    const source = boundedText(raw, name, 64);
    if (!source) return "";
    const timestamp = new Date(source);
    if (!Number.isFinite(timestamp.getTime())) throw new Error(`${name} is invalid`);
    return timestamp.toISOString();
  }

  /** @param {Record<string, unknown>} input */
  function normalizeFilters(input) {
    const kinds = boundedList(
      input.kinds,
      "kinds",
      /^(process|file|connection|dns|risk|coverage)$/
    );
    const operations = boundedList(
      input.operations,
      "operations",
      /^[a-z][a-z0-9._-]{0,127}$/
    );
    const executions = boundedList(
      input.executions,
      "executions",
      /^exec_[A-Za-z0-9_-]{8,124}$/
    );
    const risks = boundedList(
      input.risks,
      "risks",
      /^(risk_[A-Za-z0-9_-]{8,124}|[a-z][a-z0-9.-]{2,127})$/
    );
    const path = boundedText(input.path, "path", 4096);
    let domain = boundedText(input.domain, "domain", 253).toLowerCase();
    domain = domain.replace(/\.$/, "");
    const ip = boundedText(input.ip, "ip", 64);
    const from = normalizedTime(input.from, "from");
    const to = normalizedTime(input.to, "to");
    if (from && to && Date.parse(to) < Date.parse(from)) {
      throw new Error("to must not precede from");
    }
    return {
      kinds,
      operations,
      executions,
      risks,
      path,
      domain,
      ip,
      from,
      to
    };
  }

  /** @param {Record<string, unknown>} owner @param {Object} filters */
  function eventQuery(owner, filters) {
    return Object.assign({}, owner, {
      limit: 500,
      kind: filters.kinds,
      operation: filters.operations,
      execution: filters.executions,
      risk: filters.risks,
      path: filters.path,
      domain: filters.domain,
      ip: filters.ip,
      from: filters.from,
      to: filters.to
    });
  }

  /**
   * A cursor carries the canonical filter digest server-side. Sending only the
   * exact owner and cursor prevents a browser from silently changing filters
   * between pages; Manager rejects owner/filter/revision mismatch.
   * @param {Record<string, unknown>} owner
   * @param {string} cursor
   */
  function cursorQuery(owner, cursor) {
    if (!cursor || cursor.length > 4096) throw new Error("cursor is invalid");
    return Object.assign({}, owner, {cursor, limit: 500});
  }

  /** @param {Record<string, unknown>} owner @param {Object} filters */
  function summaryQuery(owner, filters) {
    return Object.assign({}, owner, {from: filters.from, to: filters.to});
  }

  /** @param {Record<string, unknown>} owner @param {Object} filters */
  function coverageQuery(owner, filters) {
    return Object.assign({}, owner, {from: filters.from, to: filters.to});
  }

  /** @param {Record<string, unknown>} owner @param {Object} filters */
  function risksQuery(owner, filters) {
    return Object.assign({}, owner, {
      from: filters.from,
      to: filters.to,
      execution: filters.executions
    });
  }

  /** @param {Object} summary @param {Array<Object>} coverage @param {Object} filters */
  function retainedGapView(summary, coverage, filters) {
    const reasons = [];
    if (summary && summary.pruned) reasons.push("retained history was pruned");
    if (summary && summary.corrupt) reasons.push("retained history is corrupt");
    for (const reason of summary && summary.reasons || []) reasons.push(reason);
    if ((coverage || []).some((value) => value.retentionGap)) {
      reasons.push("one or more coverage intervals contain a retention gap");
    }
    const range = summary && summary.retainedRange || {};
    if (filters && filters.from && range.from &&
        Date.parse(filters.from) < Date.parse(range.from)) {
      reasons.push("requested start precedes retained history");
    }
    if (filters && filters.to && range.to &&
        Date.parse(filters.to) > Date.parse(range.to)) {
      reasons.push("requested end follows retained history");
    }
    return {
      partial: reasons.length > 0,
      reasons: Array.from(new Set(reasons)),
      from: range.from || "",
      to: range.to || ""
    };
  }

  /** @param {Object} record @param {Object} detail */
  function correlate(record, detail) {
    const subject = record && record.subject || {};
    const actor = record && (record.actor || record.mediator) || {};
    const executionID = actor.executionId || subject.executionId || "";
    const flattened = flattenExecutions(detail.executions || []);
    const executionEntry = flattened.find(
      (entry) => entry.node && entry.node.execution &&
        entry.node.execution.id === executionID
    );
    const risks = (detail.risks || []).filter(
      (finding) => Array.isArray(finding.evidenceRefs) &&
        finding.evidenceRefs.includes(record.id)
    );
    const coverage = (detail.coverage || []).find(
      (interval) => interval.id === record.coverageId
    ) || null;
    return {
      event: eventView(record),
      execution: executionEntry ? executionView(executionEntry.node) : null,
      risks: risks.map(riskView),
      coverage: coverage ? coverageView(coverage) : null
    };
  }

  root.Activity = Object.freeze({
    summarize,
    ownerForSession,
    ownerQuery,
    eventView,
    newestFirst,
    flattenExecutions,
    executionView,
    coverageView,
    riskView,
    operationView,
    retentionView,
    normalizeFilters,
    eventQuery,
    cursorQuery,
    summaryQuery,
    coverageQuery,
    risksQuery,
    retainedGapView,
    correlate
  });
})();
