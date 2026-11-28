import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { buildSync } from "esbuild";

const { outputFiles } = buildSync({
  entryPoints: [fileURLToPath(new URL("main.js", import.meta.url))],
  bundle: true,
  format: "esm",
  write: false,
});
const { run } = await import(`data:text/javascript;base64,${Buffer.from(outputFiles[0].contents).toString("base64")}`);
const book = {
  id: 7, title: "小王子", authors: ["安东尼·德·圣埃克苏佩里"],
  languages: ["zho"], identifiers: { isbn: "7-5327-2896-X" },
};

function mockForm(t, reply) {
  const form = t.mock.fn(reply);
  t.mock.method(globalThis, "fetch", () => assert.fail("unexpected network request"));
  globalThis.dalibre = { form, error: (kind, message) => new Error(message, { cause: kind }) };
  t.after(() => delete globalThis.dalibre);
  return form;
}

function edition(uuid, isbn, subject, publisher, translator, url) {
  return {
    uuid, isbn, title: book.title, author: book.authors, category: "book",
    pub_house: publisher, translator: [translator], pub_year: 2002,
    external_resources: [{ url: url ?? `https://book.douban.com/subject/${subject}/` }],
  };
}

test("pasting a book link stores only its subject ID, without a network request", async (t) => {
  const form = mockForm(t, () => ({ link: " https://book.douban.com/subject/1099975/?from=search#info " }));
  const existing = { ...book, identifiers: { ...book.identifiers, douban: "1003078" } };
  assert.deepEqual(await run("bind", { books: [existing] }), {
    changes: [{ book: 7, metadata: { identifiers: { douban: "1099975" } } }],
  });
  assert.equal(form.mock.calls[0].arguments[0].fields[0].value, "https://book.douban.com/subject/1003078/");
  await assert.rejects(run("bind", { books: [book, book] }), /请选择一本书/);
});

test("pasting a search page or a different host is rejected", async (t) => {
  let link = "https://book.douban.com.example/subject/1099975/";
  mockForm(t, () => ({ link }));
  await assert.rejects(run("bind", { books: [book] }), /图书页链接/);
  link = "https://search.douban.com/book/subject_search?search_text=小王子";
  await assert.rejects(run("bind", { books: [book] }), /图书页链接/);
});

test("ISBN lookup accepts ISBN-10 equivalents and asks which matching entry to bind", async (t) => {
  const form = mockForm(t, () => ({ edition: "1099975" }));
  const items = [
    edition("one", "9787532728961", "1099975", "上海译文出版社", "周克希"),
    edition("two", "753272896X", "1099976", "测试出版社", "另一译者"),
    edition("duplicate", "753272896X", "1099975", "上海译文出版社", "周克希"),
    edition("wrong", "9787505715660", "1003078", "中国友谊出版公司", "胡雨苏"),
    edition("spoof", "9787532728961", "", "", "", "https://example.test/book.douban.com/subject/999/"),
  ];
  const fetch = t.mock.method(globalThis, "fetch", async () => Response.json({ data: items }));
  assert.deepEqual(await run("lookup", { books: [book] }), {
    changes: [{ book: 7, metadata: { identifiers: { douban: "1099975" } } }],
  });
  assert.equal(new URL(fetch.mock.calls[0].arguments[0]).searchParams.get("query"), "9787532728961");
  const options = form.mock.calls[0].arguments[0].fields[0].options;
  assert.deepEqual(options.map((o) => o.value), ["1099975", "1099976"]);
  assert.match(options[0].group, /圣埃克苏佩里/);
  assert.match(options[0].title, /上海译文出版社.*周克希.*2002/);
  assert.equal(options[0].badge, "9787532728961");
});

test("a title match cannot replace a different ISBN", async (t) => {
  const form = mockForm(t, () => assert.fail("no candidate should be shown"));
  const other = edition("wrong", "9787505715660", "1003078", "中国友谊出版公司", "胡雨苏");
  t.mock.method(globalThis, "fetch", async () => Response.json({ data: [other] }));
  const result = await run("lookup", { books: [book] });
  assert.match(result.message, /没有找到同 ISBN/);
  assert.equal(result.changes, undefined);
  assert.equal(form.mock.callCount(), 0);
});

test("books with no ISBN can use a confirmed title-and-author candidate", async (t) => {
  const form = mockForm(t, () => ({ edition: "1003078" }));
  const other = edition("one", "9787505715660", "1003078", "中国友谊出版公司", "胡雨苏");
  const long = { ...edition("long", "0".repeat(30), "1003079", "", ""), title: "𠮷".repeat(60) };
  const fetch = t.mock.method(globalThis, "fetch", async () => Response.json({ data: [other, long] }));
  const result = await run("lookup", { books: [{ ...book, title: `《${book.title}》`, identifiers: {} }] });
  assert.equal(new URL(fetch.mock.calls[0].arguments[0]).searchParams.get("query"), `${book.title} ${book.authors[0]}`);
  assert.equal(result.changes[0].metadata.identifiers.douban, "1003078");
  const options = form.mock.calls[0].arguments[0].fields[0].options;
  const fitted = options.find((o) => o.value === "1003079");
  assert.equal([...fitted.group].length, 40);
  assert.equal(fitted.badge.length, 24);
});
