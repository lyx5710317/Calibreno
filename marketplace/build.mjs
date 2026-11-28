// Bundles each plugin's source (src/<name>/main.js, with src/shared and
// npm packages) into plugins/<name>/main.js. marketplace.json is written
// by index.mjs.
//
//   node build.mjs            build every plugin
//   node build.mjs epub-merge build one

import { build } from "esbuild";
import { copyFile, cp, mkdir, readdir, stat } from "node:fs/promises";
import { existsSync } from "node:fs";


const only = process.argv.slice(2);
const names = (await readdir("plugins")).filter((n) => !n.startsWith(".") && (only.length === 0 || only.includes(n)));

for (const name of names) {
  const entry = `src/${name}/main.js`;
  if (!existsSync(entry)) continue;
  if (name === "japanese-furigana") {
    // Keep dictionary data in its original compressed form, outside the
    // script so opening the Plugins window doesn't load the dictionary.
    const assets = `plugins/${name}/assets`;
    await mkdir(assets, { recursive: true });
    await cp("node_modules/kuromoji/dict", `${assets}/ipadic`, { recursive: true });
    await copyFile("node_modules/kuromoji/LICENSE-2.0.txt", `${assets}/kuromoji-license.txt`);
    await copyFile("node_modules/kuromoji/NOTICE.md", `${assets}/ipadic-notice.txt`);
    await copyFile("node_modules/doublearray/LICENSE.txt", `${assets}/doublearray-license.txt`);
  }
  await build({
    entryPoints: [entry],
    bundle: true,
    format: "esm",
    target: "es2023",
    external: ["node:*"],
    outfile: `plugins/${name}/main.js`,
    legalComments: "none",
    banner: { js: `// Built from marketplace/src/${name}. Edit the source, not this file.` },
    logLevel: "warning",
  });
  const { size } = await stat(`plugins/${name}/main.js`);
  console.log(`${name}: ${(size / 1024).toFixed(0)} KB`);
}
