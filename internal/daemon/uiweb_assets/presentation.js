// @ts-check
"use strict";

(() => {
  const root = window.HideoutConsole = window.HideoutConsole || {};
  const DOM_ROW_LIMIT = 200;
  const DIALOG_ROW_LIMIT = 100;
  const INPUT_TEXT_LIMIT = 4096;
  const OUTPUT_TEXT_LIMIT = 8192;

  /** @param {number} codePoint */
  function mustEscape(codePoint) {
    return codePoint <= 0x1f ||
      (codePoint >= 0x7f && codePoint <= 0x9f) ||
      codePoint === 0x061c ||
      (codePoint >= 0x200b && codePoint <= 0x200f) ||
      (codePoint >= 0x2028 && codePoint <= 0x202e) ||
      (codePoint >= 0x2060 && codePoint <= 0x206f) ||
      (codePoint >= 0xd800 && codePoint <= 0xdfff) ||
      codePoint === 0xfeff ||
      (codePoint >= 0xfff9 && codePoint <= 0xfffb);
  }

  /** @param {string} character */
  function escapedCharacter(character) {
    switch (character) {
      case "\n":
        return "\\n";
      case "\r":
        return "\\r";
      case "\t":
        return "\\t";
      default: {
        const codePoint = character.codePointAt(0) || 0;
        return `\\u{${codePoint.toString(16).toUpperCase().padStart(4, "0")}}`;
      }
    }
  }

  /**
   * Converts untrusted operator data into bounded, visibly escaped text.
   * HTML metacharacters intentionally remain literal because callers assign
   * only through textContent/value, never an HTML parser.
   *
   * @param {unknown} value
   * @param {number=} limit
   */
  function safeText(value, limit = INPUT_TEXT_LIMIT) {
    const source = String(value === undefined || value === null ? "" : value);
    const characters = Array.from(source);
    const inputLimit = Number.isInteger(limit) && limit > 0 ?
      Math.min(limit, INPUT_TEXT_LIMIT) : INPUT_TEXT_LIMIT;
    let output = "";
    let consumed = 0;
    let truncated = characters.length > inputLimit;
    for (const character of characters) {
      if (consumed >= inputLimit) break;
      consumed++;
      const codePoint = character.codePointAt(0) || 0;
      const next = mustEscape(codePoint) ?
        escapedCharacter(character) : character;
      if (output.length + next.length > OUTPUT_TEXT_LIMIT) {
        truncated = true;
        break;
      }
      output += next;
    }
    return truncated ? `${output}… [truncated]` : output;
  }

  /** @param {unknown} value */
  function valueLabel(value) {
    if (value === undefined || value === null || value === "") return "—";
    let rendered;
    try {
      if (Array.isArray(value)) {
        if (!value.length) return "—";
        rendered = value.every((entry) =>
          entry === null ||
          ["string", "number", "boolean"].includes(typeof entry)
        ) ? value.map((entry) => String(entry)).join(", ") :
          JSON.stringify(value);
      } else if (typeof value === "object") {
        rendered = JSON.stringify(value);
      } else {
        rendered = String(value);
      }
    } catch {
      rendered = "[unrenderable value]";
    }
    return safeText(rendered);
  }

  /**
   * @template T
   * @param {Array<T>} values
   * @param {number=} limit
   * @returns {{items:Array<T>,omitted:number,total:number}}
   */
  function bounded(values, limit = DOM_ROW_LIMIT) {
    const source = Array.isArray(values) ? values : [];
    const maximum = Number.isInteger(limit) && limit > 0 ?
      Math.min(limit, DOM_ROW_LIMIT) : DOM_ROW_LIMIT;
    return {
      items: source.slice(0, maximum),
      omitted: Math.max(0, source.length - maximum),
      total: source.length
    };
  }

  root.Presentation = Object.freeze({
    DOM_ROW_LIMIT,
    DIALOG_ROW_LIMIT,
    safeText,
    valueLabel,
    bounded
  });
})();
