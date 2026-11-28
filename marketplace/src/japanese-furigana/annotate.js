// @ts-check

import { elementary, common } from "./kanji.js";
import { escape } from "../shared/epub.js";

const han = /\p{Script=Han}/u;
const kana = /[ぁ-ゖァ-ヺ]/u;
const hiragana = /^[ぁ-ゖゝゞー]+$/u;
const marks = new Set(["々", "〆", "〇"]);
const elementaryKanji = new Set(elementary);
const commonKanji = new Set(common);
const protectedTags = new Set("ruby rt rp rtc nav script style textarea pre code kbd samp svg math audio video iframe".split(" "));
const voidTags = new Set("area base br col embed hr img input link meta param source track wbr".split(" "));
const blockTags = new Set("address article aside blockquote br center dd div dl dt figcaption figure footer h1 h2 h3 h4 h5 h6 header hr li main ol p section table td th tr ul".split(" "));

/** @typedef {"full"|"standard"|"rare"} Mode */
/** @typedef {{ surface_form: string, reading?: string, word_type: string }} Word */
/** @typedef {{ start: number, end: number, reading: string }} Ruby */

/** @param {string} s */
function toHiragana(s) {
  return s.normalize("NFKC").replace(/[ァ-ヶヽヾ]/gu, (c) => String.fromCharCode(c.charCodeAt(0) - 0x60));
}

/** Determine the language declared by an EPUB or XHTML element. */
export function languageOf(/** @type {string} */ raw) {
  const primary = raw.trim().toLowerCase().split(/[-_]/)[0];
  if (primary === "ja" || primary === "jpn") return "ja";
  return primary && primary !== "und" && primary !== "mul" ? "other" : "";
}

/** @param {string} text @param {Mode} mode */
export function needsReading(text, mode) {
  for (const c of text) {
    if (!han.test(c) || marks.has(c)) continue;
    if (mode === "full" ||
        !((mode === "rare" ? commonKanji : elementaryKanji).has(c.normalize("NFC")))) {
      return true;
    }
  }
  return false;
}

/** @param {string} surface @param {string} reading @returns {Ruby | undefined} */
function rubySpan(surface, reading) {
  let start = 0, end = surface.length, left = 0, right = reading.length;
  // Keep the kana already printed before or after the kanji outside ruby.
  while (start < end && kana.test(surface[start]) && toHiragana(surface[start]) === reading[left]) {
    start++;
    left++;
  }
  while (end > start && right > left && kana.test(surface[end - 1]) && toHiragana(surface[end - 1]) === reading[right - 1]) {
    end--;
    right--;
  }
  return right > left && han.test(surface.slice(start, end)) ? { start, end, reading: reading.slice(left, right) } : undefined;
}

/** @param {string} text @param {Mode} mode @param {(s:string) => Word[]} tokenize */
export function readings(text, mode, tokenize) {
  /** @type {Ruby[]} */
  const spans = [];
  let at = 0, skipped = 0;
  for (const word of tokenize(text)) {
    const base = word.surface_form;
    if (!base || !text.startsWith(base, at)) return { spans: [], skipped: 1 };
    if (needsReading(base, mode)) {
      const reading = word.word_type === "KNOWN" && word.reading && toHiragana(word.reading);
      const span = reading && hiragana.test(reading) && rubySpan(base, reading);
      if (span) spans.push({ start: at + span.start, end: at + span.end, reading: span.reading });
      else skipped++;
    }
    at += base.length;
  }
  return at === text.length ? { spans, skipped } : { spans: [], skipped: 1 };
}

/** @param {ReturnType<typeof dalibre.html.tokens>} tokens */
function stripOurRuby(tokens) {
  const raw = [];
  let removed = 0;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    const [base, rt, reading, endRt, endRuby] = tokens.slice(i + 1, i + 6);
    if (t.type === "start" && t.name === "ruby" && t.attrs?.["data-calibreno-furigana"] === "1" &&
        base?.type === "text" && rt?.type === "start" && rt.name === "rt" &&
        reading?.type === "text" && endRt?.type === "end" && endRt.name === "rt" &&
        endRuby?.type === "end" && endRuby.name === "ruby") {
      raw.push(base.raw);
      removed++;
      i += 5;
    } else {
      raw.push(t.raw);
    }
  }
  return { html: raw.join(""), removed };
}

/** Rewrite complete words without moving any inline HTML tags.
 * @param {string} source @param {ReturnType<typeof languageOf>} defaultLanguage
 * @param {Mode} mode @param {(s:string) => Word[]} tokenize
 */
export function annotateDocument(source, defaultLanguage, mode, tokenize) {
  let tokens = dalibre.html.tokens(source);
  if (tokens.map((t) => t.raw).join("") !== source) throw dalibre.error("invalid", "无法完整读取 EPUB 的正文标签。");
  const restored = stripOurRuby(tokens);
  if (restored.removed) tokens = dalibre.html.tokens(restored.html);
  const output = tokens.map((t) => t.raw);
  const stack = [{ name: "", language: defaultLanguage, blocked: false, body: false }];
  /** @type {{ index: number, text: string }[]} */
  let parts = [];
  let added = 0, skipped = 0;
  function flush() {
    if (parts.length === 0) return;
    const text = parts.map((p) => p.text).join("");
    const language = stack[stack.length - 1].language;
    if (han.test(text) && (language === "ja" || !language && kana.test(text))) {
      const result = readings(text, mode, tokenize);
      skipped += result.skipped;
      let at = 0, next = 0;
      for (const part of parts) {
        const end = at + part.text.length;
        let rest = 0, html = "";
        while (next < result.spans.length && result.spans[next].start < end) {
          const span = result.spans[next++];
          if (span.end > end) {
            skipped++;
            continue;
          }
          const from = span.start - at;
          const to = span.end - at;
          html += escape(part.text.slice(rest, from)) +
            `<ruby data-calibreno-furigana="1">${escape(part.text.slice(from, to))}<rt>${span.reading}</rt></ruby>`;
          rest = to;
          added++;
        }
        if (rest) output[part.index] = html + escape(part.text.slice(rest));
        at = end;
      }
    }
    parts = [];
  }
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    const current = stack[stack.length - 1];
    if (t.type === "text") {
      if (current.body && !current.blocked && current.language !== "other") parts.push({ index: i, text: t.text ?? "" });
      continue;
    }
    if (t.type === "start" || t.type === "self") {
      const language = languageOf(t.attrs?.["xml:lang"] ?? t.attrs?.lang ?? "") || current.language;
      const blocked = current.blocked || protectedTags.has(t.name);
      if (blockTags.has(t.name) || blocked !== current.blocked || language !== current.language) flush();
      if (t.type === "start" && !voidTags.has(t.name)) {
        stack.push({ name: t.name, language, blocked, body: current.body || t.name === "body" });
      }
    } else if (t.type === "end") {
      const index = stack.findLastIndex((f) => f.name === t.name);
      if (index < 1) continue;
      const parent = stack[index - 1];
      if (blockTags.has(t.name) || current.body !== parent.body ||
          current.blocked !== parent.blocked || current.language !== parent.language) flush();
      stack.length = index;
    } else {
      flush();
    }
  }
  flush();
  return { html: output.join(""), added, removed: restored.removed, skipped };
}
