// @ts-check

import { writeFile } from "node:fs/promises";
import { attr, build, dc, openEpub, parseEpub, resolve, setText, spineDocs, text } from "../shared/epub.js";
import { annotateDocument, languageOf } from "./annotate.js";
import { localTokenizer } from "./reading.js";

const choices = [
  { value: "full", title: "全量注音（N5 参考）" },
  { value: "standard", title: "标准注音（N2 参考）" },
  { value: "rare", title: "生僻注音（N1 参考）" },
];

/** @param {string} _action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(_action, input) {
  if (!dalibre.assets?.read) throw dalibre.error("invalid", "请使用支持插件词典资源的 Calibreno 0.8.2 或更新版本。");
  const answer = await dalibre.form({
    submit: "添加假名",
    note: "处理选中书籍的 EPUB，完成后可撤销。",
    fields: [{ key: "range", title: "注音范围", type: "select", value: "standard", options: choices }],
  });
  const mode = answer.range === "full" || answer.range === "rare" ? answer.range : "standard";
  /** @type {NonNullable<Dalibre.ActionResult["formats"]>} */
  const formats = [];
  let count = 0, skipped = 0;
  for (const [index, book] of input.books.entries()) {
    const file = input.files?.[book.id];
    if (!file || file.format !== "EPUB") continue;
    const epub = await openEpub(file.path);
    const documents = spineDocs(epub).filter(({ item }) =>
      item && !/\bnav\b/.test(item.props) && /^(application\/xhtml\+xml|text\/html)$/i.test(item.type));
    const encrypted = new Set([...text(epub, "META-INF/encryption.xml").matchAll(/<(?:\w+:)?CipherReference\b[^>]*>/gi)]
      .map(([tag]) => resolve("", attr(tag, "URI"))));
    if (documents.some(({ item }) => item && encrypted.has(item.path))) {
      throw dalibre.error("invalid", `《${book.title}》的 EPUB 正文受保护，无法添加注音。`);
    }
    const languages = dc(epub, "language").map(languageOf);
    const defaultLanguage = languages.length === 1 ? languages[0] : "";
    const modified = new Set();
    for (const { item } of documents) {
      if (!item || modified.has(item.path)) continue;
      const original = text(epub, item.path);
      if (!original || /<\?xml\b[^>]*encoding\s*=\s*["'](?!utf-?8["'])/i.test(original)) continue;
      const result = annotateDocument(original, defaultLanguage, mode, (s) => localTokenizer().tokenize(s));
      skipped += result.skipped;
      if (result.html === original) continue;
      setText(epub, item.path, result.html);
      modified.add(item.path);
      count += result.added;
    }
    dalibre.progress(index + 1, input.books.length);
    if (!modified.size) continue;

    const bytes = build(epub);
    const checked = parseEpub(bytes);
    if (checked.opf !== epub.opf || [...modified].some((path) => !checked.files[path])) {
      throw dalibre.error("invalid", `《${book.title}》的输出 EPUB 校验失败。`);
    }
    const path = `/work/furigana-${book.id}.epub`;
    await writeFile(path, bytes);
    formats.push({ book: book.id, path });
  }
  const notice = skipped ? ` 有 ${skipped} 处跳过，可能缺少可靠读音或跨越排版标签。` : "";
  if (!formats.length) return { message: `所选书籍的 EPUB 无需更新。${notice}` };
  const summary = count ? `已按所选范围写入 ${count} 处注音。` : "已移除本插件在所选范围外生成的注音。";
  return { formats, message: summary + notice };
}
