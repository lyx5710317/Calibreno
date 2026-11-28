import assert from "node:assert/strict";
import { test } from "node:test";
import { run } from "./main.js";

function book(id, title, fields = {}) {
  return { id, title, authors: [], formats: [], identifiers: {}, ...fields };
}

test("proposes only scoped title and author formatting changes", () => {
  const selected = [
    book(1, "　《城南旧事》.epub ", { authors: [" 林海音　"] }),
    book(2, "三体　　黑暗森林.kepub.epub", { formats: ["EPUB"] }),
    book(3, "围城.PDF", { formats: ["pdf"] }),
  ];
  const result = run("review", { books: selected });
  assert.deepEqual(result, {
    changes: [
      { book: 1, metadata: { title: "城南旧事", authors: ["林海音"] } },
      { book: 2, metadata: { title: "三体 黑暗森林" } },
      { book: 3, metadata: { title: "围城" } },
    ],
  });
  assert.equal(selected[0].title, "　《城南旧事》.epub ");
  assert.deepEqual(selected[0].authors, [" 林海音　"]);
  const updated = selected.map((b, i) => ({ ...b, ...result.changes[i].metadata }));
  assert.deepEqual(run("review", { books: updated }), { message: "所选书目无需校对。" });
});

test("keeps compound titles, meaningful credits and ambiguous suffixes", () => {
  const selected = [
    book(1, "《三体》《黑暗森林》"),
    book(2, "《〈红楼梦〉新论》"),
    book(3, "《文学.pdf》", { formats: ["PDF"] }),
    book(4, "围城.pdf", { formats: ["EPUB"] }),
    book(5, "围城.txt.pdf", { formats: ["TXT", "PDF"] }),
    book(6, "Desert.pdf", { formats: ["PDF"] }),
    book(7, "小王子", { authors: ["安托万·德·圣埃克苏佩里"] }),
  ];
  assert.deepEqual(run("review", { books: selected }), { message: "所选书目无需校对。" });
});

test("keeps a suffix inside a book title after removing an outer suffix", () => {
  const selected = book(1, "《文学.pdf》.pdf", { formats: ["PDF"] });
  const result = run("review", { books: [selected] });
  assert.deepEqual(result, { changes: [{ book: 1, metadata: { title: "《文学.pdf》" } }] });
  const updated = { ...selected, ...result.changes[0].metadata };
  assert.deepEqual(run("review", { books: [updated] }), { message: "所选书目无需校对。" });
});

test("reports editions, volumes, translators and invalid ISBNs without rewriting them", () => {
  const selected = [
    book(1, "《小王子（修订版） 上册》.epub", {
      authors: ["安托万·德·圣埃克苏佩里", "周克希 译"],
      identifiers: { isbn: "978-0-306-40615-8", other: "keep" },
    }),
    book(2, "围城", { identifiers: { isbn: "ISBN-13：９７８-０-３０６-４０６１５-７" } }),
    book(3, "城南旧事", { identifiers: { isbn: "ISBN-10: 0-8044-2957-X" } }),
    book(4, "紅樓夢（第二卷）", { authors: ["曹雪芹（譯）"] }),
    book(5, "三体", { identifiers: { isbn: "ISBN-10: 9780306406157" } }),
    book(6, "王国", { identifiers: { isbn: "4006381333931" } }),
    book(7, "插图与文学的完整结构", { identifiers: { isbn: "ISBN 101234567X" } }),
  ];
  assert.deepEqual(run("review", { books: selected }), {
    changes: [{ book: 1, metadata: { title: "小王子（修订版） 上册" } }],
    groups: [{
      title: "需要人工复核",
      books: [
        { id: 1, note: "版次信息、卷次信息、译者信息、ISBN 格式或校验位异常" },
        { id: 4, note: "卷次信息、译者信息" },
        { id: 5, note: "ISBN 格式或校验位异常" },
        { id: 6, note: "ISBN 格式或校验位异常" },
      ],
    }],
  });
});
