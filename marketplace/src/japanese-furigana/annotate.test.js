import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { gunzipSync } from "node:zlib";
import { needsReading, readings } from "./annotate.js";
import { common, elementary } from "./kanji.js";
import { localTokenizer } from "./reading.js";

globalThis.dalibre = { assets: {
  read(name, encoding) {
    assert.equal(encoding, "gzip");
    const path = new URL(`../../node_modules/kuromoji/dict/${name.split("/").pop()}`, import.meta.url);
    const bytes = gunzipSync(readFileSync(path));
    return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
  },
} };

test("the published character tables define the three cumulative ranges", () => {
  assert.equal(new Set(elementary).size, 1026);
  assert.equal(new Set(common).size, 2136);
  for (const c of elementary) assert.ok(common.includes(c));
  const words = ["学校", "挨拶", "薔薇"];
  assert.deepEqual(words.filter((w) => needsReading(w, "full")), words);
  assert.deepEqual(words.filter((w) => needsReading(w, "standard")), words.slice(1));
  assert.deepEqual(words.filter((w) => needsReading(w, "rare")), words.slice(2));
  assert.equal(needsReading("人々", "rare"), false);
});

test("the local dictionary uses whole-word readings and leaves okurigana outside ruby", () => {
  const text = "学校で薔薇を見て、挨拶した。大人が食べる。";
  const tokenize = (s) => localTokenizer().tokenize(s);
  const result = readings(text, "full", tokenize);
  const ruby = result.spans.map(({ start, end, reading }) => [text.slice(start, end), reading]);
  assert.deepEqual(ruby, [
    ["学校", "がっこう"], ["薔薇", "ばら"], ["見", "み"], ["挨拶", "あいさつ"], ["大人", "おとな"], ["食", "た"],
  ]);
  assert.equal(result.skipped, 0);
});

test("unknown readings and mismatched tokenizer offsets are skipped", () => {
  const result = readings("𪚥", "full", () => [{ surface_form: "𪚥", word_type: "UNKNOWN" }]);
  assert.deepEqual(result, { spans: [], skipped: 1 });
  const wrong = readings("学校", "full", () => [{ surface_form: "学校です", reading: "ガッコウデス", word_type: "KNOWN" }]);
  assert.deepEqual(wrong, { spans: [], skipped: 1 });
});
