import { BrowserWindow, session } from 'electron';
import { randomUUID } from 'node:crypto';
import { open, unlink } from 'node:fs/promises';
import { readEpubForPrint } from '../src/read-epub-export.js';
import { printFontResource, printFontStyles } from './epub-print-fonts.js';

const printResources = new Map();
let printSession;
const printOrigin = 'https://epub-print.invalid';
const mediaTypes = { jpg: 'image/jpeg', png: 'image/png', gif: 'image/gif', webp: 'image/webp' };
const printPolicy = "default-src 'none'; script-src 'none'; img-src 'self'; font-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

const stylesheet = `
  @page { size: A4 portrait; margin: 18mm 17mm; }
  body { margin: 0; font: 11pt/1.6 "Noto Serif TC Variable", serif; color: black; line-break: strict; overflow-wrap: anywhere; }
  :lang(ja), body:lang(ja) { font-family: "Noto Serif JP Variable", "Noto Serif TC Variable", serif; }
  .chapter + .chapter { break-before: page; }
  h1, h2, h3, h4, h5, h6 { break-after: avoid-page; line-height: 1.3; }
  p { margin: 0 0 .7em; widows: 2; orphans: 2; }
  figure { margin: .8em 0; } img { max-width: 100%; max-height: 246mm; object-fit: contain; }
  pre { white-space: pre-wrap; } table { max-width: 100%; border-collapse: collapse; }
  th, td { border: .5pt solid #999; padding: .2em .4em; }
  a { color: inherit; } rt { font-size: 50%; }
`;

function getPrintSession() {
  if (printSession) return printSession;
  const isolated = session.fromPartition('epub-print', { cache: false });
  // Serve only generated documents and validated in-archive raster assets, entirely from memory.
  isolated.protocol.handle('https', async request => {
    if (request.method !== 'GET') return new Response(null, { status: 405 });
    if (request.url.startsWith(printOrigin + '/fonts/')) {
      return await printFontResource(new URL(request.url).pathname) || new Response(null, { status: 404 });
    }
    const resource = printResources.get(request.url);
    if (!resource) return new Response(null, { status: 404 });
    return new Response(resource.data, { headers: { 'Content-Type': resource.type,
      'Content-Security-Policy': printPolicy, 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff' } });
  });
  isolated.webRequest.onBeforeRequest((details, done) => done({
    cancel: !printResources.has(details.url) && !details.url.startsWith(printOrigin + '/fonts/'),
  }));
  isolated.setPermissionCheckHandler(() => false);
  isolated.setPermissionRequestHandler((_contents, _permission, respond) => respond(false));
  printSession = isolated;
  return isolated;
}

async function render(book, signal) {
  const base = `${printOrigin}/${randomUUID()}/`;
  const address = base + 'book';
  const title = book.title.replace(/[&<>]/gu, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[ch]);
  const lang = /^[a-zA-Z][a-zA-Z0-9-]{0,40}$/u.test(book.language) ? ` lang="${book.language}"` : '';
  const html = `<!doctype html><html${lang}><head><meta charset="utf-8"><title>${title}</title><style>${printFontStyles()}${stylesheet}</style></head><body>${book.sections.join('')}</body></html>`;
  const window = new BrowserWindow({ show: false, focusable: false, skipTaskbar: true, webPreferences: {
    session: getPrintSession(), sandbox: true, contextIsolation: true, nodeIntegration: false,
    devTools: false, backgroundThrottling: false, plugins: false, spellcheck: false,
  } });
  const contents = window.webContents;
  const stopped = Promise.withResolvers();
  const onAbort = () => stopped.reject(signal.reason);
  try {
    printResources.set(address, { data: html, type: 'text/html; charset=utf-8' });
    for (const asset of book.assets) {
      printResources.set(base + asset.name, { data: asset.data, type: mediaTypes[asset.name.split('.').at(-1)] });
    }
    contents.setWindowOpenHandler(() => ({ action: 'deny' }));
    contents.on('will-frame-navigate', event => { if (!event.isMainFrame || event.url !== address) event.preventDefault(); });
    contents.once('render-process-gone', () => stopped.reject(new Error('PDF 排版进程已退出。')));
    signal.throwIfAborted();
    signal.addEventListener('abort', onAbort, { once: true });
    await Promise.race([window.loadURL(address), stopped.promise]);
    const loaded = await Promise.race([contents.executeJavaScriptInIsolatedWorld(1001, [{
      code: "void document.body.offsetHeight; document.fonts.ready.then(faces => !Array.from(faces).some(face => face.status === 'error'))",
    }]), stopped.promise]);
    if (!loaded) throw new Error('PDF 内置字体加载失败，请检查桌面安装。');
    signal.throwIfAborted();
    return await Promise.race([contents.printToPDF({
      pageSize: 'A4', preferCSSPageSize: true, printBackground: true, displayHeaderFooter: false,
      generateDocumentOutline: true,
    }), stopped.promise]);
  } finally {
    signal.removeEventListener('abort', onAbort);
    window.destroy();
    printResources.delete(address);
    for (const asset of book.assets) printResources.delete(base + asset.name);
  }
}

export async function epubPdfRenderer({ input, output, signal, title, author, language }) {
  const timeout = AbortSignal.timeout(180_000);
  const active = signal ? AbortSignal.any([signal, timeout]) : timeout;
  let created = false;
  try {
    const book = await readEpubForPrint(input, { signal: active, title, author, language });
    active.throwIfAborted();
    const pdf = await render(book, active);
    active.throwIfAborted();
    if (pdf.subarray(0, 5).toString() !== '%PDF-' || !pdf.subarray(-32).includes('%%EOF')) {
      throw new Error('PDF 排版结果不完整。');
    }
    const handle = await open(output, 'wx');
    created = true;
    try { await handle.writeFile(pdf, { signal: active }); }
    finally { await handle.close(); }
    active.throwIfAborted();
  } catch (error) {
    if (created) await unlink(output).catch(() => {});
    signal?.throwIfAborted();
    if (timeout.aborted) throw new Error('EPUB 转 PDF 超时。', { cause: error });
    if (/^[\p{Script=Han}]|^EPUB\s|^PDF\s/u.test(error.message)) throw error;
    throw new Error('EPUB 转 PDF 失败，请检查原件和可用磁盘空间。', { cause: error });
  }
}
