import { open, unlink } from 'node:fs/promises';
import { Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { DOMParser, XMLSerializer } from '@xmldom/xmldom';
import yauzl from 'yauzl';
import yazl from 'yazl';
import { epubCrc32 } from './epub-crc.js';

const mime = 'application/epub+zip';
const zipOrigin = 'https://epub.invalid';
const maxMetadata = 2 * 1024 * 1024;
const maxChapter = 8 * 1024 * 1024;
const maxEdits = 64 * 1024 * 1024;
const maxArchive = 2 * 1024 * 1024 * 1024;
const xhtml = 'http://www.w3.org/1999/xhtml';
const xml = 'http://www.w3.org/XML/1998/namespace';
const rendition = 'http://www.idpf.org/vocab/rendition/#';
const fixedLayout = '这本 EPUB 使用固定版式，无法进行重排转换。';
const protectedBook = '这本 EPUB 包含加密或数字签名，无法转换排版。';
const layoutStyleId = 'zlib-layout-override';
const layoutModes = new Map([
  ['horizontal', { mode: 'horizontal-tb', progression: 'ltr' }],
  ['vertical-rtl', { mode: 'vertical-rl', progression: 'rtl' }],
]);
const nonText = new Set(['canvas', 'nav', 'noscript', 'script', 'style', 'svg', 'template']);
const fontAlgorithms = new Set(['http://www.idpf.org/2008/embedding', 'http://ns.adobe.com/pdf/enc#RC']);

function children(node, name) {
  return Array.from(node?.childNodes || []).filter(child =>
    child.nodeType === 1 && child.localName === name && child.namespaceURI === node.namespaceURI);
}

function parseXml(data, type = 'application/xml') {
  const utf16le = data[0] === 0xff && data[1] === 0xfe || data[0] === 0x3c && data[1] === 0;
  const utf16be = data[0] === 0xfe && data[1] === 0xff || data[0] === 0 && data[1] === 0x3c;
  const source = new TextDecoder(utf16le ? 'utf-16le' : utf16be ? 'utf-16be' : 'utf-8', { fatal: true }).decode(data);
  if (/<!ENTITY\b/i.test(source)) throw new Error('EPUB 使用了不受支持的 XML 实体声明。');
  return new DOMParser({ onError: () => { throw new Error('EPUB 中存在无法读取的 XML 文档。'); } })
    .parseFromString(source, type);
}

function encodeXml(document) {
  const source = new XMLSerializer().serializeToString(document);
  return Buffer.from(source.replace(/^<\?xml\s+[^?]*\?>/i, header => header.replace(
    /\bencoding\s*=\s*(['"])[^'"]*\1/i, 'encoding="UTF-8"')));
}

function resolveUrl(reference, base = zipOrigin + '/') {
  if (!reference || /^(?:[\\/]|\s*[a-z][a-z\d+.-]*:)/i.test(reference) || reference.includes('\\')) {
    throw new Error('EPUB 包含不可读取的外部或无效文件引用。');
  }
  const url = new URL(reference, base);
  if (url.origin !== zipOrigin) throw new Error('EPUB 包含不可读取的外部或无效文件引用。');
  return url;
}

function entryLocation(url) {
  const parts = url.pathname.slice(1).split('/').map(decodeURIComponent);
  if (parts.some(part => !part || part === '.' || part === '..' || /[\\/\0]/.test(part))) {
    throw new Error('EPUB 包含无效的文件路径。');
  }
  return parts.join('/');
}

function textContent(node) {
  const pending = [node];
  while (pending.length) {
    const current = pending.pop();
    if ((current.nodeType === 3 || current.nodeType === 4) && /[\p{L}\p{N}]/u.test(current.data)) return true;
    if (current.nodeType === 1 && !nonText.has(current.localName) && !current.hasAttribute('hidden')) {
      for (let child = current.firstChild; child; child = child.nextSibling) pending.push(child);
    }
  }
  return false;
}

async function readEntry(archive, entry, limit, signal) {
  signal?.throwIfAborted();
  if (!entry) throw new Error('EPUB 缺少引用的文件。');
  if (entry.uncompressedSize > limit) throw new Error('EPUB 的目录或章节超过排版转换大小限制。');
  const stream = await verifiedStream(archive, entry);
  const abort = () => stream.destroy(signal.reason);
  signal?.addEventListener('abort', abort, { once: true });
  try {
    signal?.throwIfAborted();
    const chunks = [];
    for await (const chunk of stream) {
      signal?.throwIfAborted();
      chunks.push(chunk);
    }
    return Buffer.concat(chunks);
  } finally {
    signal?.removeEventListener('abort', abort);
    stream.destroy();
  }
}

async function verifiedStream(archive, entry) {
  const source = await archive.openReadStreamPromise(entry);
  let crc = 0;
  const stream = new Transform({
    transform(chunk, _encoding, done) {
      crc = epubCrc32(chunk, crc);
      done(null, chunk);
    },
    flush(done) {
      done(crc !== entry.crc32 ? new Error('EPUB 资源数据校验失败，文件可能已损坏。') : undefined);
    },
  });
  stream.once('close', () => source.destroy());
  source.once('error', error => stream.destroy(error));
  source.pipe(stream);
  return stream;
}

async function listEntries(archive, signal, {
  limit = maxArchive, tooLarge = 'EPUB 解压后超过 2 GB，无法转换排版。', protectionError = protectedBook,
} = {}) {
  if (archive.entryCount > 25000) throw new Error('EPUB 文件条目过多。');
  const entries = new Map();
  let size = 0;
  for await (const entry of archive.eachEntry()) {
    signal?.throwIfAborted();
    if (/[\u0000-\u001f]/u.test(entry.fileName)) throw new Error('EPUB 包含无效的文件路径。');
    if (entries.has(entry.fileName)) throw new Error('EPUB 包含重复的文件条目。');
    if (entry.isEncrypted()) throw new Error(protectionError);
    if (!entry.canDecodeFileData()) throw new Error('EPUB 使用了不受支持的 ZIP 压缩方式。');
    size += entry.uncompressedSize;
    if (size > limit) throw new Error(tooLarge);
    entries.set(entry.fileName, entry);
  }
  return entries;
}

async function checkEpubProtection(entries, read, protectionError = protectedBook) {
  if (entries.has('META-INF/rights.xml') || entries.has('META-INF/signatures.xml')) throw new Error(protectionError);
  const fonts = new Set();
  if (entries.has('META-INF/encryption.xml')) {
    const document = parseXml(await read('META-INF/encryption.xml'));
    const blocks = [...document.getElementsByTagNameNS('http://www.w3.org/2001/04/xmlenc#', 'EncryptedData')];
    if (!blocks.length) throw new Error(protectionError);
    for (const block of blocks) {
      const algorithm = children(block, 'EncryptionMethod')[0]?.getAttribute('Algorithm');
      const references = children(children(block, 'CipherData')[0], 'CipherReference');
      const uri = references[0]?.getAttribute('URI');
      if (!fontAlgorithms.has(algorithm) || !uri || references.length !== 1) throw new Error(protectionError);
      const file = entryLocation(resolveUrl(uri));
      if (!entries.has(file) || !/\.(?:otf|ttf|ttc|woff2?)$/i.test(file)) throw new Error(protectionError);
      fonts.add(file);
    }
  }
  return fonts;
}

export async function checkReflowProtection(entries, read) {
  const fonts = await checkEpubProtection(entries, read);
  const appleOptions = 'META-INF/com.apple.ibooks.display-options.xml';
  if (entries.has(appleOptions)) {
    const document = parseXml(await read(appleOptions));
    if ([...document.getElementsByTagNameNS('*', 'option')].some(option =>
      option.getAttribute('name') === 'fixed-layout' && option.textContent.trim().toLowerCase() === 'true')) {
      throw new Error(fixedLayout);
    }
  }
  return fonts;
}

export function checkReflowLayout(book, opf, refs) {
  const prefixes = ['rendition'];
  for (const [, prefix, vocabulary] of (book.getAttribute('prefix') || '').matchAll(/(?:^|\s)([\w.-]+):\s+(\S+)/g)) {
    if (vocabulary === rendition && !prefixes.includes(prefix)) prefixes.push(prefix);
  }
  if (children(opf, 'meta').some(meta => (
    prefixes.some(prefix => meta.getAttribute('property') === prefix + ':layout') && meta.textContent.trim() === 'pre-paginated'
    || meta.getAttribute('name') === 'fixed-layout' && meta.getAttribute('content')?.toLowerCase() === 'true'
  )) || refs.some(ref => (ref.getAttribute('properties') || '').split(/\s+/).some(property =>
    prefixes.some(prefix => property === prefix + ':layout-pre-paginated')))) {
    throw new Error(fixedLayout);
  }
}

function packageInfo(document, filename, mode) {
  const book = document.documentElement;
  if (book.localName !== 'package') throw new Error('EPUB 的封装文档无效。');
  const [opf] = children(book, 'metadata');
  const [manifest] = children(book, 'manifest');
  const [spine] = children(book, 'spine');
  const refs = children(spine, 'itemref');
  if (!manifest || !refs.length) throw new Error('EPUB 缺少资源清单或阅读顺序。');
  checkReflowLayout(book, opf, refs);
  const version = book.getAttribute('version');
  if (version && !/^([23])(?:\.|$)/.test(version)) throw new Error('EPUB 版本不支持排版转换。');
  if (!/^2(?:\.|$)/.test(version)) {
    spine.setAttribute('page-progression-direction', mode.progression);
    const modified = children(opf, 'meta').find(meta => meta.getAttribute('property') === 'dcterms:modified');
    if (modified) modified.textContent = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
  }
  const legacyMode = children(opf, 'meta').find(meta => meta.getAttribute('name') === 'primary-writing-mode');
  if (legacyMode) legacyMode.setAttribute('content', mode.mode);

  const items = new Map(children(manifest, 'item').map(item => [item.getAttribute('id'), item]));
  const bookUrl = new URL(zipOrigin + '/' + filename.split('/').map(encodeURIComponent).join('/'));
  const bases = [book, manifest].map(node => node.getAttributeNS(xml, 'base')).filter(Boolean);
  const manifestBase = bases.reduce((base, value) => resolveUrl(value, base), bookUrl);
  const chapters = new Map();
  for (const reference of refs) {
    let item = items.get(reference.getAttribute('idref'));
    const seen = new Set();
    while (item && !['application/xhtml+xml', 'text/html'].includes(item.getAttribute('media-type'))) {
      if (seen.has(item)) throw new Error('EPUB 的备用章节存在循环引用。');
      seen.add(item);
      if (item.getAttribute('media-type') === 'image/svg+xml' && !item.getAttribute('fallback')) break;
      item = items.get(item.getAttribute('fallback'));
    }
    if (!item) throw new Error('EPUB 的阅读顺序包含缺失或不支持的章节。');
    const assetBase = item.getAttributeNS(xml, 'base');
    const file = entryLocation(resolveUrl(item.getAttribute('href'), assetBase ? resolveUrl(assetBase, manifestBase) : manifestBase));
    chapters.set(file, {
      svg: item.getAttribute('media-type') === 'image/svg+xml',
      nav: (item.getAttribute('properties') || '').split(/\s+/).includes('nav'),
    });
  }
  return chapters;
}

function setChapterLayout(document, mode) {
  const root = document.documentElement;
  const [body] = children(root, 'body');
  if (root.localName !== 'html' || ![null, xhtml].includes(root.namespaceURI) || !body) {
    throw new Error('EPUB 包含不支持的正文文档。');
  }
  const newElement = type => document.createElementNS(root.namespaceURI, root.prefix ? root.prefix + ':' + type : type);
  let [head] = children(root, 'head');
  if (!head) root.insertBefore(head = newElement('head'), body);
  if (children(head, 'meta').some(meta => meta.getAttribute('name')?.toLowerCase() === 'viewport'
    && /\bwidth\s*=\s*\d+\b/i.test(meta.getAttribute('content'))
    && /\bheight\s*=\s*\d+\b/i.test(meta.getAttribute('content')))) throw new Error(fixedLayout);
  const declarations = `-epub-writing-mode:${mode.mode} !important;-webkit-writing-mode:${mode.mode} !important;writing-mode:${mode.mode} !important;`;
  const style = children(head, 'style').find(node => node.getAttribute('id') === layoutStyleId) || newElement('style');
  style.setAttribute('id', layoutStyleId);
  style.setAttribute('type', 'text/css');
  style.textContent = `html,html body{${declarations}}`;
  head.appendChild(style);
  for (const node of [root, body]) {
    const inlineStyle = node.getAttribute('style') || '';
    const current = inlineStyle.replace(/(?:^|;)-epub-writing-mode:(?:horizontal-tb|vertical-rl) !important;-webkit-writing-mode:(?:horizontal-tb|vertical-rl) !important;writing-mode:(?:horizontal-tb|vertical-rl) !important;$/u, '');
    node.setAttribute('style', (current ? current.replace(/;\s*$/, '') + ';' : '') + declarations);
  }
  return textContent(body);
}

async function prepareEdits(entries, archive, mode, signal) {
  const read = (file, limit = maxMetadata) => readEntry(archive, entries.get(file), limit, signal);
  if ((await read('mimetype', 64)).toString('ascii') !== mime) throw new Error('文件不是可转换的 EPUB。');
  const fonts = await checkReflowProtection(entries, read);
  const container = parseXml(await read('META-INF/container.xml'));
  const rootfiles = children(children(container.documentElement, 'rootfiles')[0], 'rootfile')
    .filter(file => ['', 'application/oebps-package+xml'].includes(file.getAttribute('media-type') || ''));
  if (!rootfiles.length) throw new Error('EPUB 缺少封装文档。');

  const edits = new Map();
  let size = 0;
  function addEdit(file, document) {
    if (fonts.has(file)) throw new Error(protectedBook);
    const data = encodeXml(document);
    size += data.length;
    if (size > maxEdits) throw new Error('EPUB 正文超过排版转换大小限制。');
    edits.set(file, data);
  }
  for (const rootfile of rootfiles) {
    const file = entryLocation(resolveUrl(rootfile.getAttribute('full-path')));
    if (edits.has(file)) continue;
    const book = parseXml(await read(file));
    const chapters = packageInfo(book, file, mode);
    addEdit(file, book);
    let hasText = false;
    for (const [chapter, { svg, nav }] of chapters) {
      signal?.throwIfAborted();
      if (!entries.has(chapter)) throw new Error('EPUB 缺少引用的正文文件。');
      if (svg) continue;
      const document = parseXml(edits.get(chapter) || await read(chapter, maxChapter), 'application/xhtml+xml');
      if (setChapterLayout(document, mode) && !nav) hasText = true;
      if (!edits.has(chapter)) addEdit(chapter, document);
    }
    if (!hasText) throw new Error('EPUB 未找到可重排的正文，可能是图片版。');
  }
  return edits;
}

async function writeArchive(output, archive, entries, edits, signal) {
  signal?.throwIfAborted();
  // Reserve the output before writing so a failed operation can remove only its own file.
  const file = await open(output, 'wx');
  const zip = new yazl.ZipFile();
  let active;
  let complete = false;
  zip.on('error', error => zip.outputStream.destroy(error));
  try {
    zip.addBuffer(Buffer.from(mime), 'mimetype', { compress: false, forceDosTimestamp: true });
    for (const [name, entry] of entries) {
      if (name === 'mimetype') continue;
      signal?.throwIfAborted();
      const options = { mtime: entry.getLastModDate() };
      if (name.endsWith('/')) zip.addEmptyDirectory(name, options);
      else if (edits.has(name)) zip.addBuffer(edits.get(name), name, options);
      else {
        zip.addReadStreamLazy(name, { ...options, size: entry.uncompressedSize, compress: entry.compressionMethod === 8 }, callback => {
          void verifiedStream(archive, entry).then(stream => {
            if (zip.outputStream.destroyed) return stream.destroy();
            active = stream;
            stream.once('error', error => zip.outputStream.destroy(error));
            callback(null, stream);
          }, callback);
        });
      }
    }
    const written = pipeline(zip.outputStream, file.createWriteStream(), { signal });
    zip.end();
    await written;
    complete = true;
  } finally {
    active?.destroy();
    await file.close().catch(() => {});
    if (!complete) await unlink(output).catch(() => {});
  }
}

export async function convertEpubLayout({ input, output, layout, signal }) {
  const mode = layoutModes.get(layout);
  if (!mode) throw new Error('请选择横排或右起竖排。');
  signal?.throwIfAborted();
  let archive;
  try {
    archive = await yauzl.openPromise(input, { autoClose: false, strictFileNames: true });
    const entries = await listEntries(archive, signal);
    const edits = await prepareEdits(entries, archive, mode, signal);
    await writeArchive(output, archive, entries, edits, signal);
  } catch (error) {
    signal?.throwIfAborted();
    if (error.code === 'EEXIST') throw new Error('输出文件已存在，请选择新的位置。', { cause: error });
    const detail = /^(?:EPUB\s+)?[\u3400-\u9fff]/u.test(error.message) ? error.message : '请检查文件内容或目标目录权限。';
    throw new Error(`无法转换 EPUB 排版。${detail}`, { cause: error });
  } finally {
    archive?.close();
  }
}
