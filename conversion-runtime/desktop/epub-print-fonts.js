import { readFileSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(import.meta.url);
const files = new Map();
let styles;

// Only the font files referenced by the two bundled stylesheets are served to the print session.
export function printFontStyles() {
  if (styles !== undefined) return styles;
  styles = ['noto-serif-tc', 'noto-serif-jp'].map(family => {
    const stylesheet = require.resolve('@fontsource-variable/' + family);
    return readFileSync(stylesheet, 'utf8').replace(/url\(\.\/files\/([a-z0-9-]+\.woff2)\)/g, (_match, name) => {
      const route = '/fonts/' + name;
      files.set(route, path.join(path.dirname(stylesheet), 'files', name));
      return 'url(' + route + ')';
    });
  }).join('\n');
  return styles;
}

export async function printFontResource(pathname) {
  const file = files.get(pathname);
  return file ? new Response(await readFile(file), { headers: { 'Content-Type': 'font/woff2' } }) : null;
}
