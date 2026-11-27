import { createWriteStream, constants } from 'node:fs';
import { open } from 'node:fs/promises';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { randomUUID } from 'node:crypto';
import { DOMParser } from '@xmldom/xmldom';
import { parse } from 'parse5';
import yauzl from 'yauzl';
import yazl from 'yazl';
import { epubCrc32 } from './epub-crc.js';

const textLimit = 64 * 1024 * 1024;
const chapterLimit = 8 * 1024 * 1024;
const zipType = 'application/epub+zip';
const xmlHeader = '<?xml version="1.0" encoding="UTF-8"?>';
const invalidCharacters = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\ufffe\uffff]/u;
const blockTags = new Set(['address', 'article', 'aside', 'blockquote', 'caption', 'dd', 'div', 'dl', 'dt', 'figcaption', 'figure',
  'footer', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'header', 'li', 'main', 'ol', 'p', 'pre', 'section', 'table', 'tr', 'ul']);
const ignoredTags = new Set(['head', 'nav', 'noscript', 'rp', 'rt', 'script', 'style', 'svg', 'template']);

function decode(data, fallback = false) {
  const first = data[0], second = data[1];
  if (first === 0xff && second === 0xfe) return new TextDecoder('utf-16le', { fatal: true }).decode(data);
  if (first === 0xfe && second === 0xff) return new TextDecoder('utf-16be', { fatal: true }).decode(data);
  try { return new TextDecoder('utf-8', { fatal: true }).decode(data); }
  catch (error) {
    if (!fallback) throw error;
    return new TextDecoder('gb18030', { fatal: true }).decode(data);
  }
}

function escapeXml(value) {
  return String(value).replace(/[&<>"']/gu, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;' }[character]));
}

function xml(data) {
  const source = decode(data);
  if (/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(source)) throw new Error('EPUB 的容器或目录声明无效。');
  return new DOMParser().parseFromString(source, 'application/xml');
}

function entryPath(parent, reference) {
  if (!reference || /^(?:[\\/]|\s*[a-z][a-z\d+.-]*:)/i.test(reference) || reference.includes('\\')) return '';
  try {
    const base = 'https://epub.invalid/' + parent.split('/').map(encodeURIComponent).join('/');
    const url = new URL(reference, base);
    if (url.origin !== 'https://epub.invalid') return '';
    const parts = url.pathname.slice(1).split('/').map(decodeURIComponent);
    if (parts.some(part => !part || part === '.' || part === '..' || /[\\/\0]/.test(part))) return '';
    return parts.join('/');
  } catch { return ''; }
}

async function readEntry(archive, entry, limit, signal) {
  signal?.throwIfAborted();
  if (!entry || entry.uncompressedSize > limit || entry.isEncrypted()) throw new Error('EPUB 缺少可读取的章节，或章节超过大小限制。');
  const stream = await archive.openReadStreamPromise(entry);
  const chunks = [];
  let bytes = 0, checksum = 0;
  const abort = () => stream.destroy(signal.reason);
  signal?.addEventListener('abort', abort, { once: true });
  try {
    signal?.throwIfAborted();
    for await (const chunk of stream) {
      bytes += chunk.length;
      if (bytes > limit) throw new Error('EPUB 的章节超过大小限制。');
      checksum = epubCrc32(chunk, checksum);
      chunks.push(chunk);
    }
    if (checksum !== entry.crc32) throw new Error('EPUB 的章节数据校验失败，文件可能已损坏。');
    return Buffer.concat(chunks, bytes);
  } finally {
    signal?.removeEventListener('abort', abort);
    stream.destroy();
  }
}

function chapterText(data, signal) {
  const source = decode(data);
  let tokens = 0;
  for (let position = source.indexOf('<'); position >= 0; position = source.indexOf('<', position + 1)) {
    if (++tokens > 120000) throw new Error('EPUB 的章节结构过于复杂。');
  }
  const parts = [];
  let pieces = [];
  function flush(preformatted = false) {
    const text = pieces.join('');
    const part = preformatted ? text.replace(/\n$/u, '') : text.replace(/[\t ]+\n|\n[\t ]+/gu, '\n').replace(/\n{3,}/gu, '\n\n').trim();
    if (part) parts.push(part);
    pieces = [];
  }
  function visit(node, depth = 0, preformatted = false) {
    signal?.throwIfAborted();
    if (depth > 160) throw new Error('EPUB 的章节结构过深，无法转换。');
    const tag = node.tagName;
    if (ignoredTags.has(tag) || node.attrs?.some(entry => entry.name === 'hidden')) return;
    if (node.nodeName === '#text') {
      pieces.push(preformatted ? node.value : node.value.replace(/[\t\r\n ]+/g, ' '));
      return;
    }
    if (tag === 'br') { pieces.push('\n'); return; }
    if (tag === 'pre' && !preformatted) {
      flush();
      for (const child of node.childNodes || []) visit(child, depth + 1, true);
      flush(true);
      return;
    }
    const block = !preformatted && blockTags.has(tag);
    if (block) pieces.push('\n\n');
    for (const child of node.childNodes || []) visit(child, depth + 1, preformatted);
    if (!preformatted && (tag === 'td' || tag === 'th')) pieces.push('\t');
    if (block) pieces.push('\n\n');
  }
  visit(parse(source));
  flush();
  return parts.join('\n\n');
}

async function readEpub(input, signal) {
  const archive = await yauzl.openPromise(input, { autoClose: false, strictFileNames: true });
  try {
    if (archive.entryCount > 25000) throw new Error('EPUB 文件条目过多。');
    const entries = new Map();
    for await (const entry of archive.eachEntry()) {
      signal?.throwIfAborted();
      if (entries.has(entry.fileName)) throw new Error('EPUB 包含重复的文件条目。');
      entries.set(entry.fileName, entry);
    }
    const read = (name, limit = 1024 * 1024) => readEntry(archive, entries.get(name), limit, signal);
    if ((await read('mimetype', 64)).toString('ascii') !== zipType) throw new Error('文件不是可读取的 EPUB。');
    const container = xml(await read('META-INF/container.xml'));
    const rootfiles = [...container.getElementsByTagNameNS('*', 'rootfile')];
    const rootfile = rootfiles.find(node => node.getAttribute('media-type') === 'application/oebps-package+xml') || rootfiles[0];
    const opf = entryPath('', rootfile?.getAttribute('full-path'));
    const book = xml(await read(opf));
    const manifest = book.getElementsByTagNameNS('*', 'manifest')[0];
    const spine = book.getElementsByTagNameNS('*', 'spine')[0];
    const items = new Map([...manifest?.getElementsByTagNameNS('*', 'item') || []].map(node => [node.getAttribute('id'), node]));
    const chapters = [];
    let bytes = 0;
    for (const reference of spine?.getElementsByTagNameNS('*', 'itemref') || []) {
      const item = items.get(reference.getAttribute('idref'));
      if (!item) throw new Error('EPUB 的阅读顺序引用了缺失文件。');
      if (/\bnav\b/.test(item.getAttribute('properties'))) continue;
      if (!['application/xhtml+xml', 'text/html'].includes(item.getAttribute('media-type'))) continue;
      const file = await read(entryPath(opf, item.getAttribute('href')), Math.min(chapterLimit, textLimit - bytes));
      bytes += file.length;
      const text = chapterText(file, signal);
      if (text) chapters.push(text);
    }
    if (!chapters.length) throw new Error('EPUB 中没有可提取的正文，可能是图片版或受保护的文件。');
    return chapters.join('\n\n') + '\n';
  } finally { archive.close(); }
}

export function bookLanguage(value) {
  const languages = { chinese: 'zh', english: 'en', japanese: 'ja', french: 'fr', german: 'de', spanish: 'es' };
  const language = Object.hasOwn(languages, value) ? languages[value] : value;
  return typeof language === 'string' && /^[a-z]{2,3}(?:-[a-z0-9]{2,8})*$/i.test(language) ? language : 'und';
}

export async function writeEpub(output, sections, { title, author, language, signal } = {}) {
  signal?.throwIfAborted();
  const identifier = 'urn:uuid:' + randomUUID();
  const modified = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
  const name = escapeXml(title?.replace(new RegExp(invalidCharacters, 'gu'), '') || '电子书');
  const creator = author?.replace(new RegExp(invalidCharacters, 'gu'), '');
  const lang = bookLanguage(language);
  const archive = new yazl.ZipFile();
  const add = (file, value, options) => archive.addBuffer(Buffer.from(value), file, { forceDosTimestamp: true, ...options });
  add('mimetype', zipType, { compress: false });
  add('META-INF/container.xml', `${xmlHeader}<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`);
  const items = [], refs = [], links = [];
  for (const [index, { text, label }] of sections.entries()) {
    signal?.throwIfAborted();
    const id = `section-${index + 1}`;
    const heading = escapeXml(label || (index ? `第 ${index + 1} 节` : title || '正文'));
    const content = text.trim().split(/\n[\t ]*\n+/u).map(part => `<p>${escapeXml(part).replaceAll('\n', '<br/>')}</p>`).join('');
    add(`OEBPS/${id}.xhtml`, `${xmlHeader}<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="${lang}" lang="${lang}"><head><title>${heading}</title><link href="layout.css" rel="stylesheet" type="text/css"/></head><body>${content}</body></html>`);
    items.push(`<item id="${id}" href="${id}.xhtml" media-type="application/xhtml+xml"/>`);
    refs.push(`<itemref idref="${id}"/>`);
    links.push(`<li><a href="${id}.xhtml">${heading}</a></li>`);
  }
  add('OEBPS/layout.css', 'p { white-space: pre-wrap; overflow-wrap: break-word; }');
  add('OEBPS/nav.xhtml', `${xmlHeader}<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="${lang}" lang="${lang}"><head><title>目录</title></head><body><nav epub:type="toc"><h1>目录</h1><ol>${links.join('')}</ol></nav></body></html>`);
  add('OEBPS/book.opf', `${xmlHeader}<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">${identifier}</dc:identifier><dc:title>${name}</dc:title>${creator ? `<dc:creator>${escapeXml(creator)}</dc:creator>` : ''}<dc:language>${lang}</dc:language><meta property="dcterms:modified">${modified}</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="layout" href="layout.css" media-type="text/css"/>${items.join('')}</manifest><spine>${refs.join('')}</spine></package>`);
  const written = pipeline(archive.outputStream, createWriteStream(output, { flags: 'wx' }), { signal });
  archive.end();
  await written;
}

function textSections(text) {
  const sections = [];
  let start = 0;
  while (text.length - start > 48000) {
    let end = text.lastIndexOf('\n', start + 48000);
    if (end <= start) end = start + 48000 - (/[\ud800-\udbff]/u.test(text[start + 47999]) ? 1 : 0);
    const part = text.slice(start, end);
    if (part.trim()) sections.push({ text: part });
    start = end;
  }
  if (text.slice(start).trim()) sections.push({ text: text.slice(start) });
  return sections;
}

export async function readTextForConversion(input, signal) {
  signal?.throwIfAborted();
  const handle = await open(input, constants.O_RDONLY | (constants.O_NOFOLLOW || 0) | (constants.O_NONBLOCK || 0));
  try {
    const source = await handle.stat();
    if (!source.isFile()) throw new Error('TXT 来源不是可读取的文件。');
    if (source.size > textLimit) throw new Error('TXT 文件超过 64 MB，无法一次转换。');
    let bytes = 0;
    const chunks = [];
    const stream = handle.createReadStream({ autoClose: false, start: 0, end: textLimit, signal });
    for await (const chunk of stream) {
      bytes += chunk.length;
      if (bytes > textLimit) throw new Error('TXT 文件超过 64 MB，无法一次转换。');
      chunks.push(chunk);
    }
    let text;
    try { text = decode(Buffer.concat(chunks, bytes), true).replace(/\r\n?/g, '\n'); }
    catch (error) {
      signal?.throwIfAborted();
      throw new Error('无法读取 TXT 编码，请保存为 UTF-8 后重试。', { cause: error });
    }
    if (!text.trim() || invalidCharacters.test(text)) throw new Error('TXT 文件为空或包含无法转换的字符。');
    return text;
  } finally { await handle.close(); }
}

export async function convertEbook({ input, output, signal, title, author, language }) {
  signal?.throwIfAborted();
  const source = path.extname(input).toLowerCase(), target = path.extname(output).toLowerCase();
  if (source === '.txt' && target === '.epub') {
    const text = await readTextForConversion(input, signal);
    await writeEpub(output, textSections(text), { title, author, language, signal });
    return;
  }
  if (source === '.epub' && target === '.txt') {
    let text;
    try { text = await readEpub(input, signal); }
    catch (error) {
      signal?.throwIfAborted();
      throw new Error(`无法提取 EPUB 正文。${error.message}`, { cause: error });
    }
    const file = await open(output, 'wx');
    try { await file.writeFile(text, { signal }); } finally { await file.close(); }
    return;
  }
  throw new Error('当前内置电子书引擎支持 TXT 与 EPUB 互转。');
}
