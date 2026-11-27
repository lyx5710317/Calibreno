import { mkdirSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { convertEbook } from './src/convert-ebook.js';
import { convertFb2 } from './src/convert-fb2.js';
import { convertKindleToEpub } from './src/convert-kindle.js';
import { convertEpubLayout } from './src/epub-layout.js';
import { convertPdfWithCheckpoints } from './src/pdf-conversion.js';

const here = path.dirname(fileURLToPath(import.meta.url));
const parent = path.dirname(here);
const requestFile = process.argv[2];
const controller = new AbortController();
const { app } = process.versions.electron && !process.env.ELECTRON_RUN_AS_NODE ? await import('electron') : {};
if (app) {
  const profile = path.join(path.dirname(requestFile), 'profile');
  mkdirSync(profile);
  app.setPath('userData', profile);
  app.commandLine.appendSwitch('disable-background-networking');
  app.on('window-all-closed', () => {});
}

async function convert(request) {
  const options = { ...request, signal: controller.signal };
  const source = path.extname(request.input).slice(1).toUpperCase();
  if (source === 'EPUB' && request.layout) {
    await convertEpubLayout(options);
  } else if (source === 'FB2') {
    await convertFb2(options);
  } else if (source === 'MOBI' || source === 'AZW3') {
    const workerPath = path.join(parent, 'kindle-runtime', 'mobitool.exe');
    await convertKindleToEpub({ ...options, format: source, workerPath });
  } else if (source === 'PDF') {
    const executable = path.join(parent, 'pdf-ocr', 'pdf-ocr.exe');
    return convertPdfWithCheckpoints({
      ...options, executable, onProgress: progress => console.error(`PDF ${progress.page}/${progress.total}`),
    });
  } else if (request.format === 'PDF') {
    await app.whenReady();
    const { epubPdfRenderer } = await import('./desktop/epub-pdf.js');
    await epubPdfRenderer(options);
  } else {
    await convertEbook(options);
  }
  return {};
}

async function main() {
  try {
    const request = JSON.parse(await readFile(requestFile, 'utf8'));
    const result = await convert(request);
    await new Promise(resolve => process.stdout.write(JSON.stringify(result) + '\n', resolve));
    if (app) app.exit(0);
  } catch (error) {
    await new Promise(resolve => process.stdout.write(JSON.stringify({ error: error.message }) + '\n', resolve));
    if (app) app.exit(1);
    else process.exitCode = 1;
  }
}

// Let Electron finish loading the entry module before waiting for ready.
void main();
