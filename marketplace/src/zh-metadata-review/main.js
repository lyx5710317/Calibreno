// @ts-check

import { checkIsbn } from "../shared/calibre.js";

const FILE_SUFFIX = /\.(kepub(?:\.epub)?|epub|mobi|azw3?|pdf|txt|fb2)$/iu;
const HAN = /\p{Script=Han}/u;
const EDITION = /(?:修订|修訂|增订|增訂|增补|增補|纪念|紀念|插图|插圖|精装|精裝|平装|平裝|完整|未删节|未刪節)[版本]|新版|重印|再版|第[０-９0-9一二三四五六七八九十百〇零]+版/u;
const VOLUME = /[上中下]{1,3}[册冊卷]|(?:第|全)[０-９0-9一二三四五六七八九十百〇零两兩]+[册冊卷部]/u;
const TRANSLATOR = /(?:译者|譯者)\s*[:：]|(?:^|\s)(?:译|譯)(?=$|\s)|[（(【\[〔][^）)】\]〕]*(?:译|譯)[）)】\]〕]/u;

/** @param {string} title */
function unwrappedTitle(title) {
  const outer = /^《([^《》〈〉]+)》$/u.exec(title);
  return outer?.[1].trim() || "";
}

/** @param {string} title @param {string[]} formats */
function cleanTitle(title, formats) {
  let cleaned = title.trim().replace(/[ \u3000]*\u3000[ \u3000]*/gu, " ");
  const suffix = FILE_SUFFIX.exec(cleaned);
  if (suffix) {
    const stem = cleaned.slice(0, suffix.index).trimEnd();
    const extension = suffix[1].toUpperCase();
    const matchingFormat = formats.some((stored) => {
      const format = stored.toUpperCase();
      return extension === format || (extension === "KEPUB.EPUB" && (format === "KEPUB" || format === "EPUB"));
    });
    if (!FILE_SUFFIX.test(stem) && (unwrappedTitle(stem) || (HAN.test(stem) && matchingFormat))) {
      cleaned = stem;
    }
  }
  const inner = unwrappedTitle(cleaned);
  return inner && !FILE_SUFFIX.test(inner) ? inner : cleaned;
}

/** @param {string} isbn */
function validIsbn(isbn) {
  const normalized = isbn.normalize("NFKC");
  const prefix = /^ISBN(?:\s*[-‐‑–—]?\s*(10|13)(?=[\s:]))?\s*:?\s*/i.exec(normalized);
  const value = normalized.slice(prefix?.[0].length ?? 0);
  if (!/^[0-9X\s\-‐‑–—]+$/iu.test(value)) return false;
  const digits = checkIsbn(value);
  if (!digits || (prefix?.[1] && Number(prefix[1]) !== digits.length)) return false;
  return digits.length === 10 || /^(978|979)/.test(digits);
}

/** @param {string} _action @param {Dalibre.ActionInput} input @returns {Dalibre.ActionResult} */
export function run(_action, input) {
  /** @type {NonNullable<Dalibre.ActionResult["changes"]>} */
  const changes = [];
  const needsReview = [];

  for (const book of input.books) {
    const title = cleanTitle(book.title, book.formats);
    const authors = book.authors.map((author) => author.trim());
    /** @type {NonNullable<Dalibre.Changes>} */
    const metadata = {};
    if (title && title !== book.title) metadata.title = title;
    if (authors.every(Boolean) && authors.some((author, index) => author !== book.authors[index])) {
      metadata.authors = authors;
    }
    if (Object.keys(metadata).length) changes.push({ book: book.id, metadata });

    const notes = [];
    if (EDITION.test(title)) notes.push("版次信息");
    if (VOLUME.test(title)) notes.push("卷次信息");
    if (TRANSLATOR.test(title) || authors.some((author) => TRANSLATOR.test(author))) {
      notes.push("译者信息");
    }
    const isbn = book.identifiers.isbn?.trim();
    if (isbn && !validIsbn(isbn)) notes.push("ISBN 格式或校验位异常");
    if (notes.length) needsReview.push({ id: book.id, note: notes.join("、") });
  }

  if (changes.length === 0 && needsReview.length === 0) {
    return { message: "所选书目无需校对。" };
  }
  /** @type {Dalibre.ActionResult} */
  const result = {};
  if (changes.length) result.changes = changes;
  if (needsReview.length) result.groups = [{ title: "需要人工复核", books: needsReview }];
  return result;
}
