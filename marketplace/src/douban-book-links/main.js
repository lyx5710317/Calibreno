// @ts-check

import { doubanSubjectID, identify } from "../../../plugins/neodb/main.js";
import { checkIsbn, normalizeIsbn } from "../shared/calibre.js";

/** @param {Dalibre.BookInfo} book @param {string} id @returns {Dalibre.ActionResult} */
function bind(book, id) {
  if (book.identifiers.douban === id) return { message: "此书已绑定这个豆瓣书页。" };
  return { changes: [{ book: book.id, metadata: { identifiers: { douban: id } } }] };
}

/** @param {Dalibre.BookInfo} book */
async function bindLink(book) {
  const old = book.identifiers.douban?.trim() ?? "";
  const id = /^[1-9][0-9]*$/.test(old) ? old : doubanSubjectID(old);
  const answer = await dalibre.form({
    submit: "预览绑定",
    note: "请粘贴与本书版本对应的豆瓣图书页链接。",
    fields: [{
      key: "link", title: "豆瓣书页链接", type: "text",
      value: id ? `https://book.douban.com/subject/${id}/` : "",
    }],
  });
  const chosen = typeof answer.link === "string" ? doubanSubjectID(answer.link) : undefined;
  if (!chosen) throw dalibre.error("invalid", "请输入 book.douban.com/subject/ 下的图书页链接。");
  return bind(book, chosen);
}

/** Return an ISBN-13 for exact comparisons, including equivalent ISBN-10s. @param {string | undefined} value */
function editionISBN(value) {
  const text = value?.normalize("NFKC").trim().replace(/^ISBN(?:-(?:10|13))?\s*:?\s*/i, "");
  if (!text || !/^[0-9Xx\s\-‐‑–—]+$/u.test(text)) return "";
  const isbn = checkIsbn(text);
  if (!isbn || (isbn.length === 13 && !/^(978|979)/.test(isbn))) return "";
  return normalizeIsbn(isbn);
}

/** Fit metadata supplied by NeoDB into the plugin form's label limits.
 * @param {string} text @param {number} limit */
function formLabel(text, limit) {
  const chars = [...text];
  return chars.length > limit ? chars.slice(0, limit - 1).join("") + "…" : text;
}

/** @param {Dalibre.BookInfo} book */
async function lookUpLink(book) {
  const isbn = editionISBN(book.identifiers.isbn);
  const title = book.title.trim().replace(/^《([^《》]+)》$/u, "$1").trim();
  const candidates = await identify({
    title,
    authors: book.authors,
    language: book.languages?.[0],
    identifiers: isbn ? { isbn } : {},
  });
  const seen = new Set();
  const options = [];
  for (const candidate of candidates) {
    const id = candidate.identifiers?.douban;
    if (!id || seen.has(id) || (isbn && editionISBN(candidate.identifiers?.isbn) !== isbn)) continue;
    seen.add(id);
    const translator = candidate.fields?.find((field) => field.label === "Translators")?.value;
    const subtitle = candidate.fields?.find((field) => field.label === "Subtitle")?.value;
    const heading = [candidate.title, subtitle, candidate.authors?.join("、")].filter(Boolean).join(" · ");
    const edition = [
      candidate.publisher,
      translator && `${translator} 译`,
      candidate.pubdate?.slice(0, 4),
    ].filter(Boolean).join(" · ");
    options.push({
      value: id,
      group: formLabel(heading, 40),
      title: edition || candidate.authors?.join("、") || `豆瓣条目 ${id}`,
      badge: formLabel(candidate.identifiers?.isbn || `豆瓣 ${id}`, 24),
    });
  }
  if (options.length === 0) {
    return { message: isbn
      ? "没有找到同 ISBN 的豆瓣书页。找到正确版本后，可用「绑定豆瓣链接」粘贴书页。"
      : "没有找到带豆瓣链接的候选版本。找到书页后，可以手动绑定。" };
  }
  const answer = await dalibre.form({
    note: isbn ? "候选与本书 ISBN 一致。请选择对应版本并确认绑定。"
      : "本书缺少有效 ISBN。请核对书名、出版社和译者后选择版本。",
    fields: [{ key: "edition", title: "选择豆瓣书籍版本", type: "list", options }],
  });
  if (typeof answer.edition !== "string" || !seen.has(answer.edition)) {
    return { message: "请选择一个版本后再绑定。" };
  }
  return bind(book, answer.edition);
}

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  const [book] = input.books;
  if (!book || input.books.length !== 1) throw dalibre.error("invalid", "请选择一本书。");
  switch (action) {
    case "bind": return bindLink(book);
    case "lookup": return lookUpLink(book);
    default: throw dalibre.error("invalid", "无法识别这个操作。");
  }
}
