import { createHash } from 'node:crypto';
import path from 'node:path';
import { DOMParser } from '@xmldom/xmldom';
import { parse } from 'parse5';
import yauzl from 'yauzl';
import { epubCrc32 } from './epub-crc.js';
import { checkReflowLayout, checkReflowProtection } from './epub-layout.js';

const origin = 'https://epub.invalid';
const xmlNamespace = 'http://www.w3.org/XML/1998/namespace';
const dcNamespace = 'http://purl.org/dc/elements/1.1/';
const encryptionNamespace = 'http://www.w3.org/2001/04/xmlenc#';
const metadataLimit = 2 * 1024 * 1024;
const chapterLimit = 8 * 1024 * 1024;
const sourceLimit = 48 * 1024 * 1024;
const assetLimit = 32 * 1024 * 1024;
const imageLimit = 8 * 1024 * 1024;
const htmlTypes = new Set(['application/xhtml+xml', 'text/html']);
const tableRowGroups = new Set(['thead', 'tbody', 'tfoot']);
const imageTypes = new Map([
  ['image/jpeg', { ext: 'jpg', signature: /^ffd8ff/ }],
  ['image/png', { ext: 'png', signature: /^89504e470d0a1a0a/ }],
  ['image/gif', { ext: 'gif', signature: /^474946383[79]61/ }],
  ['image/webp', { ext: 'webp', signature: /^52494646........57454250/ }],
]);
const ignored = new Set(['audio', 'button', 'canvas', 'embed', 'form', 'iframe', 'input', 'nav', 'noscript',
  'object', 'script', 'select', 'style', 'template', 'video']);
const inlineElements = new Set(['a', 'abbr', 'b', 'bdi', 'bdo', 'br', 'cite', 'code', 'del', 'dfn', 'em', 'i', 'img',
  'kbd', 'mark', 'q', 'rp', 'rt', 'ruby', 's', 'samp', 'small', 'span', 'strong', 'sub', 'sup', 'time', 'u', 'var', 'wbr']);
const blockTypes = new Map([
  ['p', 'paragraph'], ['address', 'paragraph'], ['blockquote', 'quote'], ['pre', 'code'],
  ['ul', 'list'], ['ol', 'list'], ['li', 'paragraph'], ['dt', 'paragraph'], ['dd', 'paragraph'],
  ['figcaption', 'caption'], ['table', 'table'], ['hr', 'separator'], ['img', 'image'], ['image', 'image'],
]);

function hashes(input) {
  return 'epub-' + createHash('sha256').update(input).digest('hex').slice(0, 24);
}

function sibling(node, name) {
  return Array.from(node?.childNodes || []).find(child => child.nodeType === 1 && child.localName === name);
}

function decode(bytes) {
  if ((bytes[0] === 0xff && bytes[1] === 0xfe) || (bytes[0] === 0x3c && bytes[1] === 0)) {
    return new TextDecoder('utf-16le', { fatal: true }).decode(bytes);
  }
  if ((bytes[0] === 0xfe && bytes[1] === 0xff) || (bytes[0] === 0 && bytes[1] === 0x3c)) {
    return new TextDecoder('utf-16be', { fatal: true }).decode(bytes);
  }
  return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
}

function packageXml(bytes) {
  const source = decode(bytes);
  if (/<!ENTITY\b/i.test(source)) throw new Error('EPUB 资源清单包含不支持的实体声明。');
  return new DOMParser({ onError: () => { throw new Error('EPUB 资源清单无法读取。'); } }).parseFromString(source, 'application/xml');
}

function entryUrl(filename) {
  return new URL(origin + '/' + filename.split('/').map(encodeURIComponent).join('/'));
}

function resolveLocal(reference, base) {
  if (!reference || !base || /^\s*[\\/]/u.test(reference) || /^\s*[a-z][\w+.-]*:/i.test(reference)
      || /[\\\u0000-\u001f]/u.test(reference)) return undefined;
  try {
    const url = new URL(reference, base);
    if (url.origin === origin) return url;
  } catch { /* Invalid references have no exported destination. */ }
  return undefined;
}

function resource(url) {
  if (!url) return undefined;
  try {
    const parts = url.pathname.slice(1).split('/').map(decodeURIComponent);
    if (parts.some(part => !part || part === '.' || part === '..' || /[/\\\u0000-\u001f]/u.test(part))) return undefined;
    return { href: parts.join('/'), fragment: decodeURIComponent(url.hash.slice(1)) };
  } catch { return undefined; }
}

async function openEntries(archive, signal) {
  if (archive.entryCount > 25000) throw new Error('EPUB 文件条目过多。');
  const entries = new Map();
  for await (const entry of archive.eachEntry()) {
    signal?.throwIfAborted();
    if (entries.has(entry.fileName) || /[\u0000-\u001f]/u.test(entry.fileName)) throw new Error('EPUB 包含重复或无效的文件条目。');
    entries.set(entry.fileName, entry);
  }
  return entries;
}

async function readEntry(archive, entry, limit, signal) {
  signal?.throwIfAborted();
  if (!entry || entry.isEncrypted() || !entry.canDecodeFileData()) throw new Error('EPUB 缺少可读取的资源，或文件受保护。');
  if (entry.uncompressedSize > limit) throw new Error('EPUB 的资源超过导出大小限制。');
  const stream = await archive.openReadStreamPromise(entry);
  const abort = () => stream.destroy(signal.reason);
  signal?.addEventListener('abort', abort, { once: true });
  try {
    signal?.throwIfAborted();
    const chunks = [];
    let crc = 0;
    let size = 0;
    for await (const chunk of stream) {
      size += chunk.length;
      if (size > limit) throw new Error('EPUB 的资源超过导出大小限制。');
      crc = epubCrc32(chunk, crc);
      chunks.push(chunk);
    }
    if (crc !== entry.crc32) throw new Error('EPUB 的资源校验失败，文件可能已损坏。');
    return Buffer.concat(chunks);
  } finally {
    signal?.removeEventListener('abort', abort);
    stream.destroy();
  }
}

function attr(node, name) {
  return node.attrs?.find(item => (item.prefix ? `${item.prefix}:${item.name}` : item.name) === name)?.value || '';
}

function words(value) { return value.trim().split(/\s+/u); }
function cleanText(value) { return value.replace(/[\t\n\r ]+/gu, ' ').trim(); }
function heading(node) { return /^h[1-6]$/u.test(node.tagName || ''); }
function semantics(node) { return words(`${attr(node, 'epub:type')} ${attr(node, 'role')}`); }
function footnote(node) {
  return node.tagName !== 'a' && [...semantics(node), ...words(attr(node, 'class'))].some(value => /^(?:doc-)?(?:footnote|endnote|rearnote)$/iu.test(value));
}
function referenceNote(node) {
  return node.tagName === 'a' && [...semantics(node), ...words(attr(node, 'class'))].some(value => /^(?:doc-)?(?:noteref|footnote-ref|footnote-reference|endnote-ref)$/iu.test(value));
}
function omit(node, forPrint = false) {
  return (ignored.has(node.tagName) && !(forPrint && node.tagName === 'nav'))
    || node.attrs?.some(item => item.name === 'hidden') || attr(node, 'aria-hidden') === 'true'
    || /(?:^|;)\s*(?:display\s*:\s*none|visibility\s*:\s*hidden)\b/i.test(attr(node, 'style'))
    || (!forPrint && semantics(node).some(value => /^(?:doc-)?(?:toc|page-list|landmarks)$/u.test(value)));
}
function fragments(node) { return [attr(node, 'id'), attr(node, 'xml:id'), node.tagName === 'a' ? attr(node, 'name') : ''].filter(Boolean); }

function descendants(node, forPrint = false) {
  const result = [];
  const visit = (current, depth) => {
    if (depth > 80) throw new Error('EPUB 的章节结构过深，无法导出。');
    result.push(current);
    for (const child of current.childNodes || []) {
      if (!omit(child, forPrint)) visit(child, depth + 1);
    }
  };
  visit(node, 0);
  return result;
}

function parseChapter(bytes) {
  const source = decode(bytes);
  let total = 0;
  for (let at = source.indexOf('<'); at >= 0; at = source.indexOf('<', at + 1)) {
    if (++total > 120000) throw new Error('EPUB 的章节结构过于复杂。');
  }
  return parse(source, { scriptingEnabled: false });
}

function bodyOf(document) {
  return document.childNodes.find(node => node.tagName === 'html')?.childNodes.find(node => node.tagName === 'body');
}

function documentTitle(document) {
  const head = document.childNodes.find(node => node.tagName === 'html')?.childNodes.find(node => node.tagName === 'head');
  const title = head?.childNodes.find(node => node.tagName === 'title');
  return title ? inline(title, {}, null).text.trim().slice(0, 300) : '';
}

function readPublication(container, book, opf, forPrint) {
  if (container.documentElement.localName !== 'container' || book.documentElement.localName !== 'package') {
    throw new Error('EPUB 的容器或封装文档无效。');
  }
  const pkg = book.documentElement;
  const metadata = sibling(pkg, 'metadata');
  const manifest = sibling(pkg, 'manifest');
  const spine = sibling(pkg, 'spine');
  const pick = name => Array.from(metadata?.getElementsByTagNameNS(dcNamespace, name) || [])
    .map(node => cleanText(node.textContent).slice(0, 500)).filter(Boolean);
  const refs = Array.from(spine?.childNodes || []).filter(node => node.localName === 'itemref');
  if (!manifest || !refs.length) throw new Error('EPUB 缺少资源清单或阅读顺序。');
  if (forPrint) checkReflowLayout(pkg, metadata, refs);
  const baseOf = (node, base) => node.hasAttributeNS(xmlNamespace, 'base')
    ? resolveLocal(node.getAttributeNS(xmlNamespace, 'base'), base) : base;
  const base = baseOf(manifest, baseOf(pkg, entryUrl(opf)));
  const items = new Map();
  for (const item of manifest.childNodes) {
    if (item.localName !== 'item') continue;
    const id = item.getAttribute('id');
    const resolved = resource(resolveLocal(item.getAttribute('href'), baseOf(item, base)));
    items.set(id, { href: resolved?.href, type: item.getAttribute('media-type'),
      fallback: item.getAttribute('fallback'), nav: words(item.getAttribute('properties') || '').includes('nav') });
  }
  const chapters = new Map();
  for (const ref of refs) {
    let item = items.get(ref.getAttribute('idref'));
    if (!item) throw new Error('EPUB 的阅读顺序引用了缺失文件。');
    const seen = new Set();
    while (item && !htmlTypes.has(item.type)) {
      if (seen.has(item)) throw new Error('EPUB 章节的备用文件存在循环引用。');
      seen.add(item);
      item = items.get(item.fallback);
    }
    if (!item?.href && forPrint) throw new Error('EPUB 包含不支持 PDF 重排的正文格式。');
    if (!item?.href || (item.nav && !forPrint)) continue;
    const exclude = ref.getAttribute('linear') === 'no';
    if (!chapters.has(item.href) || !exclude) chapters.set(item.href, exclude);
  }
  return { items, chapters, title: pick('title')[0], author: pick('creator').join('、'), language: pick('language')[0],
    nav: [...items.values()].find(item => item.nav)?.href, ncx: items.get(spine.getAttribute('toc'))?.href };
}

async function navigation(book, read) {
  const titles = new Map();
  const tocItems = [];
  const titleFor = (reference, href, title, depth) => {
    const location = resource(resolveLocal(reference, entryUrl(href)));
    if (!location || !title || tocItems.length >= 10000) return;
    title = title.slice(0, 300);
    if (!titles.has(location.href)) titles.set(location.href, title);
    tocItems.push({ ...location, title, depth: Math.max(1, depth) });
  };
  if (book.nav) {
    const body = bodyOf(parseChapter(await read(book.nav)));
    const findNav = (node, depth = 0) => {
      if (depth > 80) throw new Error('EPUB 的导航结构过深，无法导出。');
      if (node.tagName === 'nav') return semantics(node).includes('toc') ? node : undefined;
      for (const child of node.childNodes || []) {
        const found = findNav(child, depth + 1);
        if (found) return found;
      }
      return undefined;
    };
    const toc = body && findNav(body);
    if (toc) for (const node of descendants(toc)) {
      if (node.tagName !== 'a') continue;
      let depth = 0;
      for (let parent = node.parentNode; parent && parent !== toc; parent = parent.parentNode) if (['ol', 'ul'].includes(parent.tagName)) depth++;
      titleFor(attr(node, 'href'), book.nav, inline(node, {}, null).text.trim(), depth);
    }
  }
  if (!titles.size && book.ncx) {
    const ncx = packageXml(await read(book.ncx));
    for (const point of ncx.getElementsByTagNameNS('*', 'navPoint')) {
      const label = sibling(sibling(point, 'navLabel'), 'text');
      let depth = 1;
      for (let parent = point.parentNode; parent?.localName === 'navPoint'; parent = parent.parentNode) depth++;
      titleFor(sibling(point, 'content')?.getAttribute('src'), book.ncx, cleanText(label?.textContent || ''), depth);
    }
  }
  return { titles, tocItems };
}

function htmlText(value) { return value.replace(/[&<>]/gu, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[character])); }
function markdownText(value) {
  return htmlText(value).replace(/[\\`*_\[\]#|~]/gu, '\\$&').replace(/^(\s*)([+=-]|\d{1,9}[.)])(?=\s)/gu,
    (match, whitespace, marker) => whitespace + marker.slice(0, -1) + '\\' + marker.at(-1));
}
function attach(parts, separator = '') { return { text: parts.map(value => value.text).join(separator), markdown: parts.map(value => value.markdown).join(separator) }; }
function fence(value, mark, minimum) {
  let count = minimum;
  for (const run of value.matchAll(mark === '`' ? /`+/g : /~+/g)) count = Math.max(count, run[0].length + 1);
  return mark.repeat(count);
}

function inline(node, context, chapter) {
  if (omit(node)) return { text: '', markdown: '' };
  if (node.nodeName === '#text') {
    const text = node.value.replace(/[\t\r\n ]+/gu, ' ');
    return { text, markdown: markdownText(text) };
  }
  const name = node.tagName;
  if (['rt', 'rp', 'rtc'].includes(name)) return { text: '', markdown: '' };
  if (name === 'br') return { text: '\n', markdown: '  \n' };
  if (name === 'img' || name === 'image') {
    const alt = cleanText(attr(node, 'alt') || attr(node, 'title')).slice(0, 500);
    const href = chapter && resource(resolveLocal(attr(node, 'src') || attr(node, 'href') || attr(node, 'xlink:href'), chapter.base))?.href;
    const asset = context.assets?.get(href);
    const destination = asset && (context.markdownReferences?.asset(asset.name) || `assets/${asset.name}`);
    return { text: '', markdown: destination ? `![${markdownText(alt)}](${destination})` : (alt ? markdownText(alt) : '') };
  }
  const content = attach((node.childNodes || []).map(child => inline(child, context, chapter)), /^(?:table|tbody|tr|td|th)$/u.test(name || '') ? ' ' : '');
  const text = content.text;
  let markdown = content.markdown;
  if (name === 'ruby') {
    const children = (node.childNodes || []).flatMap(child => child.tagName === 'rtc' ? child.childNodes : [child]);
    if (children.some(child => child.tagName === 'rt')) {
      markdown = '<ruby>' + children.map(child => {
        if (!['rt', 'rp'].includes(child.tagName)) return htmlText(inline(child, context, chapter).text);
        return `<${child.tagName}>${htmlText(attach((child.childNodes || []).map(item => inline(item, context, chapter))).text)}</${child.tagName}>`;
      }).join('') + '</ruby>';
    }
  } else if (markdown.trim() && ['b', 'strong', 'i', 'em', 's', 'del'].includes(name)) {
    const delimiter = ['b', 'strong'].includes(name) ? '**' : ['i', 'em'].includes(name) ? '*' : '~~';
    markdown = markdown.replace(/\S(?:[\s\S]*\S)?/u, match => delimiter + match + delimiter);
  } else if (name === 'code' && text.trim()) {
    const tick = fence(text, '`', 1);
    const content = /^[` ]|[` ]$/u.test(text) ? ` ${text} ` : text;
    markdown = tick + content + tick;
  } else if (name === 'a' && chapter) {
    const href = attr(node, 'href').trim();
    const location = resource(resolveLocal(href, chapter.base));
    const target = location && context.targets.get(location.href)?.get(location.fragment);
    if (target?.note) return { text: '', markdown: context.markdownReferences?.note(target.id) || `[^${target.id}]` };
    if (target && markdown.trim()) {
      const destination = context.markdownReferences?.anchor(target.id) || `#${target.id}`;
      markdown = `[${markdown.trim()}](${destination})`;
    }
    else if (!location && /^(?:https?:\/\/|mailto:)/i.test(href) && !/[\s<>\u0000-\u001f]/u.test(href) && URL.canParse(href) && markdown.trim()) {
      markdown = `[${markdown.trim()}](<${new URL(href).href}>)`;
    }
  } else if (['p', 'li', 'div', 'blockquote'].includes(name)) {
    return { text: text + '\n', markdown: markdown + '\n' };
  }
  return { text, markdown };
}

function imageLocation(node, base) {
  return resource(resolveLocal(attr(node, 'src') || attr(node, 'href') || attr(node, 'xlink:href'), base))?.href;
}

function linkLocation(node, base) {
  if (node.tagName !== 'a') return undefined;
  return resource(resolveLocal(attr(node, 'href'), base));
}

function coordinate(href, fragment) { return `${href}\u0000${fragment}`; }

const printElements = new Set(('article section nav aside header footer address p div span figure figcaption blockquote '
  + 'h1 h2 h3 h4 h5 h6 br hr pre code kbd em strong b i u s del sub sup small mark ruby rb rt rp rtc '
  + 'ol ul li dl dt dd table caption thead tfoot tbody tr th td bdi bdo a wbr').split(' '));

// Rebuild book markup using only these elements and generated resource/anchor names.
// No source element, style declaration or arbitrary URL reaches the print renderer.
function printChapter(chapter, chapters, assets) {
  const targetId = (href, fragment) => hashes(coordinate(href, fragment));
  const languageAttribute = node => {
    const language = attr(node, 'lang') || attr(node, 'xml:lang');
    return /^[a-zA-Z][a-zA-Z0-9-]{0,40}$/u.test(language) ? ` lang="${language}"` : '';
  };
  const render = node => {
    if (omit(node, true)) return '';
    if (node.nodeName === '#text') return htmlText(node.value);
    const id = fragments(node)[0];
    let attributes = id ? ` id="${targetId(chapter.href, id)}"` : '';
    if (node.tagName === 'img' || node.tagName === 'image') {
      const asset = assets.get(imageLocation(node, chapter.base));
      const alt = htmlText(attr(node, 'alt').slice(0, 500)).replace(/"/gu, '&quot;');
      return asset ? `<img${attributes} src="./${asset.name}" alt="${alt}">` : alt;
    }
    if (!node.tagName) return '';
    const tag = printElements.has(node.tagName) ? node.tagName : 'span';
    attributes += languageAttribute(node);
    const direction = attr(node, 'dir');
    if (['auto', 'ltr', 'rtl'].includes(direction)) attributes += ` dir="${direction}"`;
    if (tag === 'a') {
      const location = linkLocation(node, chapter.base);
      if (location && chapters.has(location.href)) attributes += ` href="#${targetId(location.href, location.fragment)}"`;
    }
    if (tag === 'td' || tag === 'th') {
      for (const name of ['rowspan', 'colspan']) {
        const value = attr(node, name);
        if (/^[1-9]\d?$/u.test(value)) attributes += ` ${name}="${value}"`;
      }
    }
    if (tag === 'ol' && /^-?\d{1,8}$/u.test(attr(node, 'start'))) attributes += ` start="${attr(node, 'start')}"`;
    if (['br', 'hr', 'wbr'].includes(tag)) return `<${tag}${attributes}>`;
    return `<${tag}${attributes}>${(node.childNodes || []).map(render).join('')}</${tag}>`;
  };
  const language = languageAttribute(chapter.body) || languageAttribute(chapter.body.parentNode);
  return `<section class="chapter" id="${targetId(chapter.href, '')}"${language}>${chapter.body.childNodes.map(render).join('')}</section>`;
}

function buildBlocks(chapter, noteTargets) {
  const blocks = [];
  const usedIds = new Set();
  let pending = [];
  const isNote = node => footnote(node) || fragments(node).some(fragment => noteTargets.has(coordinate(chapter.href, fragment)));
  const add = (type, nodes, node) => {
    const nested = nodes.flatMap(node => descendants(node));
    const ids = [...new Set([...pending, ...nested.flatMap(fragments)])];
    const fragment = (node && fragments(node)[0]) || ids[0];
    const note = type === 'note';
    let localId = hashes(coordinate(chapter.href, fragment || '\u0000' + blocks.length));
    if (usedIds.has(localId)) localId = hashes(coordinate(chapter.href, '\u0000' + blocks.length));
    usedIds.add(localId);
    blocks.push({ type, nodes, node, fragments: ids, fragment, id: localId, note });
    pending = [];
  };
  function walk(node) {
    if (omit(node)) return;
    if (isNote(node)) { add('note', [node], node); return; }
    if (heading(node)) { add('heading', [node], node); return; }
    const type = blockTypes.get(node.tagName);
    if (node.tagName === 'p') {
      const first = node.childNodes.find(child => !omit(child)
        && (child.tagName || (child.nodeName === '#text' && child.value.trim())));
      if (first?.tagName === 'a' && isNote(first)) { add('note', [node], node); return; }
    }
    if (type && (type !== 'list' || !descendants(node).some(isNote))) { add(type, [node], node); return; }
    pending.push(...fragments(node));
    let inline = [];
    const flush = () => {
      if (inline.some(item => item.tagName || item.nodeName === '#text' && item.value.trim())) add('paragraph', inline, null);
      inline = [];
    };
    for (const child of node.childNodes || []) {
      if (omit(child)) continue;
      if (isNote(child) || child.tagName && (!inlineElements.has(child.tagName) || blockTypes.has(child.tagName))) {
        flush();
        walk(child);
      } else inline.push(child);
    }
    flush();
  }
  walk(chapter.body);
  return blocks;
}

function formatList(node, context, chapter, depth = 0) {
  const ordered = node.tagName === 'ol';
  const start = /^\d{1,8}$/u.test(attr(node, 'start')) ? Number(attr(node, 'start')) : 1;
  const lines = [];
  const paragraphs = [];
  for (const child of node.childNodes?.filter(item => item.tagName === 'li' && !omit(item)) || []) {
    const nested = child.childNodes.filter(item => !omit(item) && ['ol', 'ul'].includes(item.tagName));
    const content = attach(child.childNodes.filter(item => !nested.includes(item)).map(item => inline(item, context, chapter)));
    const marker = ordered ? `${start + lines.length}. ` : '- ';
    const indent = '    '.repeat(depth);
    const continuation = indent + ' '.repeat(marker.length);
    const list = nested.map(item => formatList(item, context, chapter, depth + 1));
    lines.push(indent + marker + content.markdown.trim().replace(/\n/g, '\n' + continuation) + list.map(item => '\n' + item.markdown).join(''));
    paragraphs.push(content.text.trim(), ...list.map(item => item.text));
  }
  return { text: paragraphs.filter(Boolean).join('\n'), markdown: lines.join('\n') };
}

function formatTable(node, context, chapter) {
  const isVisibleRow = child => child.tagName === 'tr' && !omit(child);
  const tableRows = node.childNodes.flatMap(child => tableRowGroups.has(child.tagName) && !omit(child)
    ? child.childNodes.filter(isVisibleRow) : (isVisibleRow(child) ? [child] : []));
  const rows = tableRows.map(row => row.childNodes.filter(cell => ['td', 'th'].includes(cell.tagName) && !omit(cell))
    .map(cell => inline(cell, context, chapter)));
  const width = rows.reduce((largest, row) => Math.max(largest, row.length), 0);
  if (!width || width > 60 || rows.length > 2000 || width * rows.length > 10000) return inline(node, context, chapter);
  const line = row => '| ' + Array.from({ length: width }, (_, index) => (row[index]?.markdown || '').trim().replace(/\n/g, '<br/>')).join(' | ') + ' |';
  const caption = node.childNodes.find(child => child.tagName === 'caption');
  const title = caption ? inline(caption, context, chapter) : null;
  const text = rows.map(row => row.map(cell => cell.text.trim()).join('\t')).join('\n');
  return { text: title ? title.text.trim() + '\n' + text : text, markdown: `${title ? title.markdown.trim() + '\n\n' : ''}${line(rows[0])}\n${line(Array.from({ length: width }, () => ({ markdown: '---' })))}\n${rows.slice(1).map(line).join('\n')}` };
}

function renderBlock(block, chapter, index, context) {
  let value;
  if (block.type === 'code') {
    const text = descendants(block.node).filter(node => node.nodeName === '#text').map(node => node.value).join('');
    const marker = fence(text, '~', 3);
    value = { text, markdown: `${marker}\n${text.replace(/\n$/u, '')}\n${marker}` };
  } else if (block.type === 'list') value = formatList(block.node, context, chapter);
  else if (block.type === 'table') value = formatTable(block.node, context, chapter);
  else {
    value = attach(block.nodes.map(node => inline(node, context, chapter)));
    if (block.type === 'heading') {
      const level = context.nativeChapterHeading && index === 0 ? 2 : Number(block.node.tagName.slice(1)) + (context.nativeChapterHeading ? 1 : 2);
      value.markdown = `${'#'.repeat(Math.min(6, level))} ${value.markdown.trim()}`;
    } else if (block.type === 'quote') value.markdown = value.markdown.trim().split('\n').map(line => '> ' + line).join('\n');
    else if (block.type === 'separator') value.markdown = '---';
  }
  let markdown = value.markdown.trim();
  const text = value.text.trim();
  if (block.note && markdown) markdown = `[^${block.id}]: ${markdown.replace(/\n/g, '\n    ')}`;
  const anchored = !block.note && (block.fragments.length || index === 0);
  if (anchored) markdown = `<a id="${block.id}"></a>${markdown ? '\n\n' + markdown : ''}`;
  if (!text && !markdown) return undefined;
  return { type: block.type, text, markdown, locator: { href: chapter.href, ...(block.fragment && { fragment: block.fragment }), block: index + 1 },
    ...(context.markdownReferences && (block.note ? { markdownNote: block.id } : {
      ...(anchored && { markdownAnchors: [block.id] }),
      ...(block.type === 'heading' && { markdownHeadingLevel: Number(block.node.tagName.slice(1)) }),
    })),
    ...(chapter.exclude || block.note || ['image', 'separator'].includes(block.type) ? { excludeFromCorpus: true } : {}) };
}

async function encryptedResources(entries, read) {
  if (entries.has('META-INF/rights.xml')) throw new Error('EPUB 含版权保护数据，无法导出。');
  if (!entries.has('META-INF/encryption.xml')) return [];
  const descriptor = packageXml(await read('META-INF/encryption.xml'));
  const references = Array.from(descriptor.getElementsByTagNameNS(encryptionNamespace, 'CipherReference'));
  const locations = references.map(node => resource(resolveLocal(node.getAttribute('URI'), entryUrl('')))?.href);
  if (!references.length || locations.some(href => !href)) throw new Error('EPUB 的加密资源信息无法读取。');
  return locations;
}

export function readEpubForExport(input, options) {
  return readEpub(input, options, false);
}

export function readEpubForPrint(input, options) {
  return readEpub(input, options, true);
}

async function readEpub(input, { signal, title, author, language, includeAssets = true, markdownReferences } = {}, forPrint) {
  signal?.throwIfAborted();
  let archive;
  try {
    archive = await yauzl.openPromise(input, { autoClose: false, strictFileNames: true });
    const entries = await openEntries(archive, signal);
    const protectedPaths = new Set();
    const read = (href, limit = metadataLimit) => {
      if (protectedPaths.has(href)) throw new Error('EPUB 包含无法导出的加密内容。');
      return readEntry(archive, entries.get(href), limit, signal);
    };
    if ((await read('mimetype', 64)).toString() !== 'application/epub+zip') throw new Error('文件不是可读取的 EPUB。');
    const encrypted = forPrint ? await checkReflowProtection(entries, read) : await encryptedResources(entries, read);
    for (const href of encrypted) protectedPaths.add(href);
    const container = packageXml(await read('META-INF/container.xml'));
    const roots = Array.from(container.getElementsByTagNameNS('*', 'rootfile'));
    const root = roots.find(node => node.getAttribute('media-type') === 'application/oebps-package+xml') || roots[0];
    const opf = resource(resolveLocal(root?.getAttribute('full-path'), entryUrl('')))?.href;
    const book = readPublication(container, packageXml(await read(opf)), opf, forPrint);
    const navigationInfo = forPrint ? undefined : await navigation(book, read);
    const byHref = new Map([...book.items.values()].filter(item => item.href).map(item => [item.href, item]));
    const chapters = new Map();
    const noteTargets = new Set();
    let sourceBytes = 0;
    let chapterNodes = 0;
    for (const [href, exclude] of book.chapters) {
      if (chapters.size >= 4000) throw new Error('EPUB 的章节过多，无法导出。');
      const bytes = await read(href, Math.min(chapterLimit, sourceLimit - sourceBytes));
      sourceBytes += bytes.length;
      const document = parseChapter(bytes);
      if (forPrint) {
        const head = document.childNodes.find(node => node.tagName === 'html')?.childNodes.find(node => node.tagName === 'head');
        if (head?.childNodes.some(node => node.tagName === 'meta' && attr(node, 'name').toLowerCase() === 'viewport'
            && /\bwidth\s*=\s*\d+/iu.test(attr(node, 'content')) && /\bheight\s*=\s*\d+/iu.test(attr(node, 'content')))) {
          throw new Error('这本 EPUB 使用固定版式，无法重排为 PDF。');
        }
      }
      const body = bodyOf(document);
      if (!body || (forPrint && omit(body, true))) continue;
      const nodes = descendants(body, forPrint);
      chapterNodes += nodes.length;
      if (chapterNodes > 300000) throw new Error('EPUB 的正文结构过于复杂。');
      const base = entryUrl(href);
      chapters.set(href, { href, base, nodes, body, exclude, fallbackTitle: documentTitle(document) });
      for (const node of nodes) {
        if (!referenceNote(node)) continue;
        const target = linkLocation(node, base);
        if (!target?.fragment) continue;
        noteTargets.add(coordinate(target.href, target.fragment));
        const item = byHref.get(target.href);
        if (item && !item.nav && htmlTypes.has(item.type) && !book.chapters.has(target.href)) book.chapters.set(target.href, true);
      }
    }
    const assets = new Map();
    let assetBytes = 0;
    for (const chapter of includeAssets ? chapters.values() : []) for (const node of chapter.nodes) {
      if (!['image', 'img'].includes(node.tagName)) continue;
      const href = imageLocation(node, chapter.base);
      if (!href || assets.has(href)) continue;
      const entry = entries.get(href);
      const mediaType = byHref.get(href)?.type;
      const format = imageTypes.get(mediaType === 'image/jpg' ? 'image/jpeg' : mediaType);
      if (!format || !entry || entry.isEncrypted() || protectedPaths.has(href)) continue;
      if (assets.size >= 600 || entry.uncompressedSize > Math.min(imageLimit, assetLimit - assetBytes)) {
        throw new Error('EPUB 的插图超过单文件或导出总量限制。');
      }
      const data = await read(href, imageLimit);
      if (!format.signature.test(data.subarray(0, 12).toString('hex'))) throw new Error('EPUB 中的插图与资源清单不匹配。');
      assetBytes += data.length;
      assets.set(href, { name: `${hashes(href)}.${format.ext}`, data });
    }
    if (forPrint) {
      if (![...chapters.values()].some(chapter => chapter.nodes.some(node => node.nodeName === '#text' && node.value.trim()))) {
        throw new Error('EPUB 中没有可重排为 PDF 的正文。');
      }
      const sections = [];
      let bytes = 0;
      for (const chapter of chapters.values()) {
        signal?.throwIfAborted();
        if (!chapter.nodes.some(node => (node.nodeName === '#text' && node.value.trim())
            || (['image', 'img'].includes(node.tagName) && assets.has(imageLocation(node, chapter.base))))) continue;
        const html = printChapter(chapter, chapters, assets);
        bytes += Buffer.byteLength(html);
        if (bytes > 96 * 1024 * 1024) throw new Error('EPUB 的排印正文超过大小限制。');
        sections.push(html);
      }
      return { title: book.title || title || path.parse(input).name, language: book.language || language || '',
        sections, assets: [...assets.values()] };
    }
    const { titles, tocItems } = navigationInfo;
    const targets = new Map();
    for (const chapter of chapters.values()) {
      signal?.throwIfAborted();
      const blocks = buildBlocks(chapter, noteTargets);
      chapter.blocks = blocks;
      const ids = new Map();
      if (blocks[0]) ids.set('', blocks[0]);
      for (const block of blocks) for (const id of block.fragments) if (!ids.has(id)) ids.set(id, block);
      targets.set(chapter.href, ids);
    }
    const sections = [];
    let outputBytes = 0;
    for (const chapter of chapters.values()) {
      signal?.throwIfAborted();
      const context = { assets, targets, markdownReferences };
      const headingBlock = chapter.blocks.find(block => block.type === 'heading');
      const firstHeading = headingBlock && inline(headingBlock.node, context, chapter).text.trim().slice(0, 300);
      const label = titles.get(chapter.href) || firstHeading || chapter.fallbackTitle || `第 ${sections.length + 1} 节`;
      context.nativeChapterHeading = chapter.blocks[0] === headingBlock && firstHeading === label;
      const blocks = chapter.blocks.map((block, index) => renderBlock(block, chapter, index, context)).filter(Boolean);
      if (!blocks.some(block => block.text || /!\[/.test(block.markdown))) continue;
      outputBytes += blocks.reduce((bytes, block) => bytes + Buffer.byteLength(block.text) + Buffer.byteLength(block.markdown), 0);
      if (outputBytes > 96 * 1024 * 1024) throw new Error('EPUB 的正文超过导出大小限制。');
      sections.push({ id: chapter.href, target: targets.get(chapter.href)?.get('')?.id, title: label, blocks,
        ...(markdownReferences && chapter.fallbackTitle && { markdownDocumentTitle: chapter.fallbackTitle }) });
    }
    if (!sections.some(section => section.blocks.some(block => block.text && !block.excludeFromCorpus))) {
      throw new Error('EPUB 中没有可提取的正文，可能是图片版或受保护的文件。');
    }
    const outputHrefs = new Set(sections.map(section => section.id));
    const toc = tocItems.flatMap(({ href, fragment, title, depth }) => {
      if (!outputHrefs.has(href)) return [];
      const ids = targets.get(href);
      const anchor = ids?.get(fragment) || (!markdownReferences && ids?.get(''));
      return anchor && !anchor.note ? [{ href, title, depth, target: anchor.id }] : [];
    });
    return { title: book.title || title || path.parse(input).name, author: book.author || author || '',
      language: book.language || language || '', sections, assets: [...assets.values()], toc };
  } catch (error) {
    signal?.throwIfAborted();
    const detail = /^[\u3400-\u9fff]|^EPUB\s+[\u3400-\u9fff]/u.test(error.message) ? error.message : '请检查 EPUB 文件是否完整。';
    throw new Error(`无法读取 EPUB。${detail}`, { cause: error });
  } finally { archive?.close(); }
}
