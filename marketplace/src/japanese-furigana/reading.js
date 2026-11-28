// @ts-check

import Tokenizer from "kuromoji/src/Tokenizer.js";
import DynamicDictionaries from "kuromoji/src/dict/DynamicDictionaries.js";

let tokenizer;

/** Load the packed IPADIC dictionary only when annotation starts. */
export function localTokenizer() {
  if (tokenizer) return tokenizer;
  const dic = new DynamicDictionaries();
  const read = (/** @type {string} */ stem) => dalibre.assets.read(`ipadic/${stem}.dat.gz`, "gzip");
  const bytes = (/** @type {string} */ stem) => new Uint8Array(read(stem));

  dic.loadTrie(new Int32Array(read("base")), new Int32Array(read("check")));
  dic.loadTokenInfoDictionaries(bytes("tid"), bytes("tid_pos"), bytes("tid_map"));
  dic.loadConnectionCosts(new Int16Array(read("cc")));
  dic.loadUnknownDictionaries(
    bytes("unk"), bytes("unk_pos"), bytes("unk_map"), bytes("unk_char"),
    new Uint32Array(read("unk_compat")), bytes("unk_invoke"),
  );
  tokenizer = new Tokenizer(dic);
  return tokenizer;
}
