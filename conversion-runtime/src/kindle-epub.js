import { randomUUID } from 'node:crypto';
import { open, unlink } from 'node:fs/promises';
import path from 'node:path';
import { Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { DOMParser } from '@xmldom/xmldom';
import { parse, parseFragment } from 'parse5';
import yauzl from 'yauzl';
import yazl from 'yazl';
import { bookLanguage } from './convert-ebook.js';
import { epubCrc32 } from './epub-crc.js';

const mib = 1024 * 1024;
const packageFile = 'OEBPS/content.opf';
const navFile = 'OEBPS/zlibrary-nav.xhtml';
const ncxFile = 'OEBPS/zlibrary-toc.ncx';
const epubType = 'application/epub+zip';
const xhtml = 'http://www.w3.org/1999/xhtml';
const epub = 'http://www.idpf.org/2007/ops';
const svg = 'http://www.w3.org/2000/svg';
const mathml = 'http://www.w3.org/1998/Math/MathML';
const opf = 'http://www.idpf.org/2007/opf';
const dc = 'http://purl.org/dc/elements/1.1/';
const ncx = 'http://www.daisy.org/z3986/2005/ncx/';
const xml = '<?xml version="1.0" encoding="UTF-8"?>';
const origin = 'https://epub.invalid';
const mediaTypes = new Map(Object.entries({
  '.html': 'application/xhtml+xml', '.xhtml': 'application/xhtml+xml', '.css': 'text/css',
  '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.png': 'image/png', '.gif': 'image/gif', '.svg': 'image/svg+xml',
  '.otf': 'font/otf', '.ttf': 'font/ttf', '.woff': 'font/woff', '.woff2': 'font/woff2',
  '.mp3': 'audio/mpeg', '.mp4': 'video/mp4',
}));
const unsafeTags = new Set(['base', 'canvas', 'embed', 'form', 'guide', 'iframe', 'input', 'noscript', 'object',
  'script', 'template', 'foreignObject']);
const voidTags = new Set(['area', 'br', 'col', 'hr', 'img', 'link', 'meta', 'source', 'track', 'wbr']);
const obsoleteStyles = { align: 'text-align', bgcolor: 'background-color', background: 'background-image',
  color: 'color', face: 'font-family', valign: 'vertical-align', hspace: 'margin-inline', vspace: 'margin-block',
  width: 'width', height: 'height', border: 'border-width', cellspacing: 'border-spacing', clear: 'clear' };
const obsoleteAttributes = new Set(['cellpadding', 'compact', 'filepos', 'mediarecindex', 'recindex', 'nowrap', 'size']);
const metadataTags = ['title', 'creator', 'language', 'identifier', 'subject', 'description', 'publisher',
  'contributor', 'date', 'type', 'format', 'source', 'relation', 'coverage', 'rights'];

function clean(text) {
  return String(text).replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\ud800-\udfff\ufffe\uffff]/gu, '');
}

function escape(text) {
  return clean(text).replace(/[&<>"']/gu, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;' }[character]));
}

function direct(element, name) {
  return Array.from(element?.childNodes || []).filter(node => node.nodeType === 1 && node.localName === name);
}

function decode(bytes, legacy = false) {
  try { return new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
  catch (error) {
    if (!legacy) throw error;
    return new TextDecoder('windows-1252').decode(bytes);
  }
}

function parseXml(bytes) {
  const source = decode(bytes);
  if (/<!ENTITY\b/iu.test(source)) throw new Error('转换引擎返回了无效的 XML 声明。');
  return new DOMParser({ onError: () => { throw new Error('转换引擎返回了无法读取的 EPUB 目录。'); } })
    .parseFromString(source, 'application/xml');
}

function target(value, filename) {
  if (!value || /^(?:[\\/]|[a-z][\w.+-]*:)|[\\\u0000-\u001f]/iu.test(value)) return null;
  try {
    const base = filename.split('/').map(encodeURIComponent).join('/');
    const resolved = new URL(value, `${origin}/${base}`);
    const pathname = decodeURIComponent(resolved.pathname.slice(1));
    if (resolved.origin !== origin || /(?:^|\/)(?:\.|\.\.)(?:\/|$)|[\\\u0000-\u001f]/u.test(pathname)) return null;
    return { file: pathname, fragment: decodeURIComponent(resolved.hash.slice(1)) };
  } catch { return null; }
}

function verifiedStream(archive, entry) {
  return archive.openReadStreamPromise(entry).then(source => {
    let crc = 0;
    const stream = new Transform({
      transform(chunk, _encoding, done) { crc = epubCrc32(chunk, crc); done(null, chunk); },
      flush(done) { done(crc !== entry.crc32 ? new Error('转换产生的 EPUB 资源校验失败。') : undefined); },
    });
    stream.once('close', () => source.destroy());
    source.once('error', error => stream.destroy(error));
    return source.pipe(stream);
  });
}

async function listEntries(archive, signal) {
  if (archive.entryCount > 12000) throw new Error('转换产生的 EPUB 文件条目过多。');
  const entries = new Map();
  let size = 0;
  for await (const entry of archive.eachEntry()) {
    signal?.throwIfAborted();
    const name = entry.fileName;
    if (!/^(?:mimetype|META-INF\/container\.xml|OEBPS\/[a-z0-9][a-z0-9_.-]*)$/iu.test(name)
        || entries.has(name.toLowerCase()) || entry.isEncrypted() || !entry.canDecodeFileData()) {
      throw new Error('转换产生的 EPUB 包含无效或受保护的资源条目。');
    }
    size += entry.uncompressedSize;
    if (size > 256 * mib) throw new Error('转换产生的 EPUB 解压后超过 256 MB。');
    entries.set(name.toLowerCase(), entry);
  }
  return entries;
}

async function readEntry(archive, entry, limit, signal) {
  signal?.throwIfAborted();
  if (!entry || entry.uncompressedSize > limit) throw new Error('转换缺少可读取的 EPUB 资源，或资源超过大小限制。');
  const stream = await verifiedStream(archive, entry);
  const abort = () => stream.destroy(signal.reason);
  signal?.addEventListener('abort', abort, { once: true });
  try {
    signal?.throwIfAborted();
    const chunks = [];
    for await (const chunk of stream) chunks.push(chunk);
    return Buffer.concat(chunks);
  } finally {
    signal?.removeEventListener('abort', abort);
    stream.destroy();
  }
}

function attrs(node) {
  return new Map(node.attrs?.map(({ name, prefix, value }) => [prefix ? `${prefix}:${name}` : name, clean(value)]));
}

function prepareMarkup(source, item, signal) {
  // In MOBI6 this is a void element. HTML parsing otherwise nests everything after each page break.
  source = source.replace(/<mbp:pagebreak\b([^<>]*?)\s*\/>/giu, '<mbp:pagebreak$1></mbp:pagebreak>');
  let tags = 0;
  for (let pos = source.indexOf('<'); pos >= 0; pos = source.indexOf('<', pos + 1)) {
    if (++tags > 240000) throw new Error('转换产生的 EPUB 章节结构过于复杂。');
  }
  const tree = item.type === 'image/svg+xml' ? parseFragment(source, { scriptingEnabled: false })
    : parse(source, { scriptingEnabled: false });
  const ids = new Map();
  const remap = [];
  const pending = [tree];
  while (pending.length) {
    signal?.throwIfAborted();
    const node = pending.pop();
    if (unsafeTags.has(node.tagName)) continue;
    const attributes = attrs(node);
    const id = attributes.get('id') || attributes.get('xml:id') || (node.tagName === 'a' ? attributes.get('name') : '');
    if (id && !ids.has(id)) {
      const anchor = { node, id };
      ids.set(id, anchor);
      if (/\s/u.test(id)) remap.push(anchor);
    }
    for (const child of (node.childNodes || []).slice().reverse()) pending.push(child);
  }
  let index = 0;
  for (const anchor of remap) {
    let replacement;
    do { replacement = `mobi-id-${++index}`; } while (ids.has(replacement));
    anchor.id = replacement;
  }
  item.tree = tree;
  item.ids = ids;
}

function resolveLink(value, from, items, { external = false, text = false } = {}) {
  const ref = target(value.trim(), from);
  if (!ref) return external && /^(?:https?:|mailto:)[^\s]+$/iu.test(value) ? value : '';
  const linked = items.get(ref.file.toLowerCase());
  if (!linked || (text && linked.type !== 'application/xhtml+xml')) return '';
  const fragment = linked.ids?.get(ref.fragment)?.id;
  const file = linked.file === from && fragment ? '' : path.posix.basename(linked.file);
  return `${file}${fragment ? '#' + encodeURIComponent(fragment) : ''}`;
}

function style(source, from, items) {
  const url = /url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"\s]*))\s*\)/giu;
  return clean(source).replace(/@import\s+(['"])([^'"\r\n]*)\1/giu, (_rule, _quote, value) => {
    const local = resolveLink(value, from, items);
    return `@import url("${local || 'data:,'}")`;
  }).replace(url, (_rule, quoted, single, bare) => {
    const value = quoted ?? single ?? bare;
    const local = resolveLink(value, from, items);
    return `url("${local || 'data:,'}")`;
  });
}

function renderMarkup(item, items, language, title) {
  let svgUsed = false, mathUsed = false;
  const hasTitle = item.tree.childNodes.find(node => node.tagName === 'html')?.childNodes
    .find(node => node.tagName === 'head')?.childNodes.some(node => node.tagName === 'title' && node.childNodes.some(child => child.value?.trim()));
  const render = (node, depth = 0, parent) => {
    if (depth > 160) throw new Error('转换产生的 EPUB 章节结构过深。');
    if (node.nodeName === '#text') return escape(parent === 'style' ? style(node.value, item.file, items) : node.value);
    const tag = node.tagName;
    if (unsafeTags.has(tag)) return '';
    if (!tag && node.nodeName !== '#document' && node.nodeName !== '#document-fragment') return '';
    const body = (node.childNodes || []).map(child => render(child, depth + 1, tag)).join('');
    if (!tag) return body;
    if (tag === 'mbp:pagebreak') return '<div style="break-before: page; page-break-before: always"/>' + body;
    if (tag.includes(':') || tag === 'reference' || (!hasTitle && tag === 'title')) return body;
    const attributes = attrs(node);
    if (tag === 'meta' && !attributes.has('name')) return '';
    if (tag === 'link' && attributes.get('rel')?.toLowerCase() !== 'stylesheet') return '';
    const id = attributes.get('id') || attributes.get('xml:id') || (tag === 'a' ? attributes.get('name') : '');
    const anchor = item.ids.get(id);
    const element = ({ center: 'div', font: 'span', strike: 's', big: 'span', tt: 'code' })[tag] || tag;
    const css = [attributes.get('style'), tag === 'center' && 'text-align: center', tag === 'big' && 'font-size: larger'].filter(Boolean);
    const result = [];
    for (const [name, original] of attributes) {
      if (/^(?:on|xmlns)/iu.test(name) || ['id', 'xml:id', 'style', 'srcset', 'ping', 'target', 'formaction', 'contenteditable'].includes(name)
          || tag === 'a' && name === 'name' || name === 'xml:base') continue;
      const html = node.namespaceURI === xhtml;
      if (html && obsoleteAttributes.has(name)) continue;
      if (html && Object.hasOwn(obsoleteStyles, name)) {
        const measured = /^(?:[hv]space|height|width|border|cellspacing)$/u.test(name);
        const unit = measured && /^\d+$/u.test(original) ? 'px' : '';
        const background = name === 'background' && resolveLink(original, item.file, items);
        if (background) css.push(`background-image: url("${background}")`);
        else if (name !== 'background') css.push(`${obsoleteStyles[name]}: ${original}${unit}`);
        if (name === 'border' && Number(original) > 0) css.push('border-style: solid');
        continue;
      }
      if (!/^(?:(?:xml|xlink|epub):)?[a-z_][\w.-]*$/iu.test(name)) continue;
      const value = /^(?:href|xlink:href|src|poster)$/u.test(name)
        ? resolveLink(original, item.file, items, { external: tag === 'a' && name === 'href' }) : original;
      if (value || !original) result.push(`${name}="${escape(value)}"`);
    }
    if (anchor?.node === node) result.push(`id="${escape(anchor.id)}"`);
    if (css.length) result.push(`style="${escape(style(css.join('; '), item.file, items))}"`);
    if (tag === 'img' && !attributes.has('alt')) result.push('alt=""');
    if (tag === 'html') {
      result.push(`xmlns="${xhtml}"`, `xmlns:epub="${epub}"`, 'xmlns:xlink="http://www.w3.org/1999/xlink"');
      if (!attributes.has('xml:lang')) result.push(`xml:lang="${escape(attributes.get('lang') || language)}"`);
    }
    if (tag === 'svg' && node.namespaceURI === svg) { svgUsed = true; result.push(`xmlns="${svg}"`, 'xmlns:xlink="http://www.w3.org/1999/xlink"'); }
    if (tag === 'math' && node.namespaceURI === mathml) { mathUsed = true; result.push(`xmlns="${mathml}"`); }
    if (tag === 'img' && !result.some(attribute => attribute.startsWith('src='))) return escape(attributes.get('alt') || '');
    if (tag === 'link' && !result.some(attribute => attribute.startsWith('href='))) return '';
    const start = `<${element}${result.length ? ' ' + result.join(' ') : ''}`;
    return voidTags.has(element) ? `${start}/>`
      : `${start}>${tag === 'head' && !hasTitle ? `<title>${escape(title)}</title>` : ''}${body}</${element}>`;
  };
  const value = xml + render(item.tree);
  item.properties = [svgUsed && 'svg', mathUsed && 'mathml'].filter(Boolean).join(' ');
  const document = parseXml(Buffer.from(value));
  const expected = item.type === 'image/svg+xml' ? svg : xhtml;
  if (document.documentElement.namespaceURI !== expected) throw new Error('转换产生的 EPUB 章节或插图无效。');
  return value;
}

function readNavigation(document, filename, items, spine) {
  const visit = (point, depth) => {
    if (depth > 64) throw new Error('源书目录层级过深。');
    const label = direct(direct(point, 'navLabel')[0], 'text')[0]?.textContent.trim();
    const href = resolveLink(direct(point, 'content')[0]?.getAttribute('src') || '', filename, items, { text: true });
    const children = direct(point, 'navPoint').flatMap(child => visit(child, depth + 1));
    return href && label ? [{ label, href, children }] : children;
  };
  const navigation = direct(direct(document?.documentElement, 'navMap')[0], 'navPoint').flatMap(point => visit(point, 0));
  return navigation.length ? navigation : spine.map((item, index) => ({
    label: index ? `第 ${index + 1} 节` : '正文', href: path.posix.basename(item.file), children: [],
  }));
}

function navigationFiles(navigation, title, language, identifier) {
  let index = 0;
  const lists = entries => `<ol>${entries.map(({ label, href, children }) => `<li><a href="${escape(href)}">${escape(label)}</a>${children.length ? lists(children) : ''}</li>`).join('')}</ol>`;
  const points = entries => entries.map(({ label, href, children }) => `<navPoint id="nav-point-${++index}"><navLabel><text>${escape(label)}</text></navLabel><content src="${escape(href)}"/>${points(children)}</navPoint>`).join('');
  const depth = entries => entries.length ? 1 + Math.max(...entries.map(entry => depth(entry.children))) : 0;
  return [
    `${xml}<html xmlns="${xhtml}" xmlns:epub="${epub}" xml:lang="${escape(language)}"><head><title>目录</title></head><body><nav epub:type="toc"><h1>目录</h1>${lists(navigation)}</nav></body></html>`,
    `${xml}<ncx xmlns="${ncx}" version="2005-1" xml:lang="${escape(language)}"><head><meta name="dtb:uid" content="${escape(identifier)}"/><meta name="dtb:depth" content="${depth(navigation)}"/><meta name="dtb:totalPageCount" content="0"/><meta name="dtb:maxPageNumber" content="0"/></head><docTitle><text>${escape(title)}</text></docTitle><navMap>${points(navigation)}</navMap></ncx>`,
  ];
}

function bookMetadata(packageNode, options) {
  const metadata = direct(packageNode, 'metadata')[0];
  const values = Object.fromEntries(metadataTags.map(tag => [tag, direct(metadata, tag).filter(node => node.namespaceURI === dc)
    .map(node => clean(node.textContent).trim()).filter(Boolean)]));
  const identifier = direct(metadata, 'identifier').find(node => node.getAttribute('id') === packageNode.getAttribute('unique-identifier'))?.textContent.trim()
    || values.identifier[0] || 'urn:uuid:' + randomUUID();
  const title = values.title[0] || clean(options.title || '电子书');
  const language = bookLanguage(values.language[0] || options.language);
  values.language = [language];
  values.title = values.title.length ? values.title : [title];
  if (!values.creator.length && options.author) values.creator = [clean(options.author)];
  values.identifier = [identifier, ...values.identifier.filter(value => value !== identifier)];
  const fields = Object.entries(values).flatMap(([tag, list]) => list.map((value, index) =>
    `<dc:${tag}${tag === 'identifier' && index === 0 ? ' id="uid"' : ''}>${escape(value)}</dc:${tag}>`)).join('');
  const coverId = direct(metadata, 'meta').find(node => node.getAttribute('name') === 'cover')?.getAttribute('content');
  return { title, author: values.creator.join('、'), language, identifier, fields, coverId };
}

async function prepareBook(archive, entries, options) {
  const read = (name, limit = 4 * mib) => readEntry(archive, entries.get(name.toLowerCase()), limit, options.signal);
  if (decode(await read('mimetype', 64)) !== epubType) throw new Error('转换引擎未生成 EPUB 包。');
  const container = parseXml(await read('META-INF/container.xml'));
  const rootfile = direct(direct(container.documentElement, 'rootfiles')[0], 'rootfile')[0];
  if (rootfile?.getAttribute('full-path') !== packageFile) throw new Error('转换产生的 EPUB 缺少封装文档。');
  const book = parseXml(await read(packageFile)).documentElement;
  if (book.localName !== 'package' || book.namespaceURI !== opf) throw new Error('转换产生的 EPUB 封装文档无效。');
  const metadata = bookMetadata(book, options);
  const items = new Map(), ids = new Map();
  const spineNode = direct(book, 'spine')[0];
  let sourceNcx;
  for (const entry of direct(direct(book, 'manifest')[0], 'item')) {
    const id = entry.getAttribute('id');
    const ref = target(entry.getAttribute('href'), packageFile);
    const file = ref && entries.get(ref.file.toLowerCase())?.fileName;
    if (!file?.startsWith('OEBPS/') || [navFile, ncxFile, packageFile].includes(file)) continue;
    if (id === spineNode?.getAttribute('toc') && path.extname(file) === '.ncx') sourceNcx = file;
    const type = mediaTypes.get(path.extname(file).toLowerCase());
    if (!type || items.has(file.toLowerCase())) continue;
    const item = { file, type, key: `resource-${items.size + 1}` };
    items.set(file.toLowerCase(), item);
    ids.set(id, item);
  }
  const spine = direct(spineNode, 'itemref').map(ref => ids.get(ref.getAttribute('idref')));
  if (!spine.length || spine.some(item => item?.type !== 'application/xhtml+xml')) throw new Error('转换产生的 EPUB 缺少完整的正文或阅读顺序。');
  let markupSize = 0;
  for (const item of items.values()) {
    if (!['application/xhtml+xml', 'image/svg+xml'].includes(item.type)) continue;
    const data = await read(item.file, 16 * mib);
    markupSize += data.length;
    if (markupSize > 64 * mib) throw new Error('转换产生的 EPUB 正文超过 64 MB。');
    prepareMarkup(decode(data, true), item, options.signal);
  }
  for (const item of items.values()) {
    options.signal?.throwIfAborted();
    if (item.tree) item.data = Buffer.from(renderMarkup(item, items, metadata.language, metadata.title));
    if (item.type === 'text/css') item.data = Buffer.from(style(decode(await read(item.file), true), item.file, items));
  }
  const cover = ids.get(metadata.coverId);
  const contents = readNavigation(sourceNcx && parseXml(await read(sourceNcx)), sourceNcx, items, spine);
  const [nav, toc] = navigationFiles(contents, metadata.title, metadata.language, metadata.identifier);
  const manifest = [...items.values()].map(item => {
    const properties = [item.type === 'application/xhtml+xml' && item.properties, item === cover && /^image\//u.test(item.type) && 'cover-image'].filter(Boolean).join(' ');
    return `<item id="${item.key}" href="${escape(path.posix.basename(item.file))}" media-type="${item.type}"${properties ? ` properties="${properties}"` : ''}/>`;
  }).join('');
  const refs = spine.map(item => `<itemref idref="${item.key}"/>`).join('');
  const modified = new Date().toISOString().replace(/\.\d+Z$/u, 'Z');
  const packageXml = `${xml}<package xmlns="${opf}" version="3.0" unique-identifier="uid"><metadata xmlns:dc="${dc}">${metadata.fields}<meta property="dcterms:modified">${modified}</meta></metadata><manifest><item id="nav" href="${path.posix.basename(navFile)}" media-type="application/xhtml+xml" properties="nav"/><item id="ncx" href="${path.posix.basename(ncxFile)}" media-type="application/x-dtbncx+xml"/>${manifest}</manifest><spine toc="ncx">${refs}</spine></package>`;
  return { items, metadata, nav, toc, packageXml };
}

async function writeArchive(output, archive, entries, book, signal) {
  signal?.throwIfAborted();
  const file = await open(output, 'wx');
  const zip = new yazl.ZipFile();
  let active, complete = false;
  const add = (name, value, compress = true) => zip.addBuffer(Buffer.isBuffer(value) ? value : Buffer.from(value), name, { compress, forceDosTimestamp: true });
  zip.on('error', error => zip.outputStream.destroy(error));
  try {
    add('mimetype', epubType, false);
    add('META-INF/container.xml', `${xml}<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="${packageFile}" media-type="application/oebps-package+xml"/></rootfiles></container>`);
    add(packageFile, book.packageXml);
    add(navFile, book.nav);
    add(ncxFile, book.toc);
    for (const { file: name, data } of book.items.values()) {
      signal?.throwIfAborted();
      if (data) add(name, data);
      else {
        const source = entries.get(name.toLowerCase());
        zip.addReadStreamLazy(name, { size: source.uncompressedSize }, callback => {
          void verifiedStream(archive, source).then(stream => {
            if (zip.outputStream.destroyed) return stream.destroy();
            active = stream;
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

export async function writeKindleEpub(input, output, options = {}) {
  const { signal } = options;
  signal?.throwIfAborted();
  const archive = await yauzl.openPromise(input, { autoClose: false, strictFileNames: true });
  try {
    const entries = await listEntries(archive, signal);
    const book = await prepareBook(archive, entries, options);
    await writeArchive(output, archive, entries, book, signal);
    const { title, author, language } = book.metadata;
    return { title, author, language };
  } finally { archive.close(); }
}
