import { randomUUID } from 'node:crypto';
import { open, unlink } from 'node:fs/promises';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { DOMParser } from '@xmldom/xmldom';
import yazl from 'yazl';

const maxSourceBytes = 64 * 1024 * 1024;
const maxImageBytes = 16 * 1024 * 1024;
const maxTextBytes = 48 * 1024 * 1024;
const maxXmlTokens = 300000;
const fbNamespace = 'http://www.gribuser.ru/xml/fictionbook/2.0';
const xlink = 'http://www.w3.org/1999/xlink';
const xml = 'http://www.w3.org/XML/1998/namespace';
const imageTypes = new Map([
  ['image/jpeg', { extension: 'jpg', header: /^ffd8ff/i }],
  ['image/png', { extension: 'png', header: /^89504e470d0a1a0a/i }],
  ['image/gif', { extension: 'gif', header: /^47494638(?:37|39)61/i }],
]);
const tags = new Map([
  ['p', 'p'], ['strong', 'strong'], ['emphasis', 'em'], ['strikethrough', 's'], ['sub', 'sub'], ['sup', 'sup'],
  ['code', 'code'], ['style', 'span'], ['poem', 'div'], ['stanza', 'div'], ['v', 'p'], ['cite', 'blockquote'],
  ['epigraph', 'blockquote'], ['text-author', 'p'], ['date', 'p'], ['annotation', 'aside'],
  ['table', 'table'], ['tr', 'tr'], ['td', 'td'], ['th', 'th'],
]);
const ignorable = new Set(['binary', 'description', 'script', 'stylesheet']);
const css = 'img{max-width:100%;height:auto}p.fb-v{margin:.12em 0}div.fb-stanza{margin:1em 0}p.fb-text-author{text-align:end}';

function escape(value) {
  return String(value ?? '').replace(/[&<>"']/g, character => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;',
  }[character]));
}

function elements(parent, name) {
  return Array.from(parent?.childNodes || []).filter(node =>
    node.nodeType === 1 && node.namespaceURI === parent.namespaceURI && (!name || node.localName === name));
}

function value(node, limit = 500) {
  return node?.textContent.replace(/\s+/gu, ' ').trim().slice(0, limit) || '';
}

function titleText(node) {
  return elements(node, 'p').map(paragraph => value(paragraph)).filter(Boolean).join(' ').slice(0, 500) || value(node);
}

function languageTag(value) {
  return typeof value === 'string' && /^[a-z]{2,3}(?:-[a-z0-9]{1,8})*$/i.test(value) ? value : '';
}

function href(node) {
  return node.getAttributeNS(xlink, 'href') || node.getAttribute('href') || '';
}

function fragment(url) {
  if (!url.startsWith('#')) return '';
  try { return decodeURIComponent(url.slice(1)); }
  catch { return url.slice(1); }
}

function prefixEncoding(data) {
  if ((data[0] === 0xff && data[1] === 0xfe) || (data[0] === 0x3c && data[1] === 0)) return 'utf-16le';
  if ((data[0] === 0xfe && data[1] === 0xff) || (data[0] === 0 && data[1] === 0x3c)) return 'utf-16be';
  return '';
}

export function matchesFb2Signature(data) {
  const encoding = prefixEncoding(data);
  const prefix = encoding ? new TextDecoder(encoding).decode(data) : data.toString('latin1').replace(/^\u00ef\u00bb\u00bf/u, '');
  const whitespace = /\s*/y;
  let position = 0;
  while (position < prefix.length) {
    whitespace.lastIndex = position;
    whitespace.exec(prefix);
    position = whitespace.lastIndex;
    const declaration = prefix.startsWith('<?xml', position);
    const comment = prefix.startsWith('<!--', position);
    if (!declaration && !comment) break;
    const suffix = declaration ? '?>' : '-->';
    const end = prefix.indexOf(suffix, position + (declaration ? 5 : 4));
    if (end === -1) return false;
    position = end + suffix.length;
  }
  return /^<(?:[\w.-]+:)?FictionBook(?=\s|\/?>)/u.test(prefix.slice(position));
}

async function readBook(input, signal) {
  signal?.throwIfAborted();
  const file = await open(input, 'r');
  try {
    const stat = await file.stat();
    if (!stat.isFile() || stat.size > maxSourceBytes) throw new Error('FB2 文件过大或不可读取。');
    const chunks = [];
    let bytes = 0;
    signal?.throwIfAborted();
    const stream = file.createReadStream({ autoClose: false, start: 0, end: maxSourceBytes, signal });
    for await (const chunk of stream) {
      bytes += chunk.length;
      if (bytes > maxSourceBytes) throw new Error('FB2 文件超过 64 MB，无法转换。');
      chunks.push(chunk);
    }
    const data = Buffer.concat(chunks);
    const prefix = data.subarray(0, 512).toString('latin1');
    const declared = /^\s*<\?xml[^?]*\bencoding\s*=\s*["']([\w.-]+)["']/i.exec(prefix)?.[1];
    let source;
    try { source = new TextDecoder(prefixEncoding(data) || declared || 'utf-8', { fatal: true }).decode(data); }
    catch (error) { throw new Error('FB2 文件的字符编码无法读取。', { cause: error }); }
    signal?.throwIfAborted();
    if (/<!\s*(?:DOCTYPE|ENTITY)\b/i.test(source)) throw new Error('FB2 包含不支持的 XML 类型或实体声明。');
    let tokens = 0;
    for (let position = source.indexOf('<'); position !== -1; position = source.indexOf('<', position + 1)) {
      if (++tokens > maxXmlTokens) throw new Error('FB2 章节结构过于复杂。');
    }
    const document = new DOMParser({ onError: () => { throw new Error('FB2 文件的 XML 结构无法读取。'); } })
      .parseFromString(source, 'application/xml');
    const root = document.documentElement;
    if (root.localName !== 'FictionBook' || ![null, fbNamespace].includes(root.namespaceURI)) {
      throw new Error('文件不是可转换的 FictionBook 2 电子书。');
    }
    return root;
  } finally { await file.close(); }
}

function scanChapters(book) {
  const chapters = [];
  for (const [index, body] of elements(book, 'body').entries()) {
    const notes = /^(?:notes|footnotes|comments)$/i.test(body.getAttribute('name') || '');
    const auxiliary = index > 0 || notes;
    const nodes = elements(body);
    const bodyTitle = titleText(elements(body, 'title')[0]);
    const sections = elements(body, 'section');
    if (!nodes.length) continue;
    const add = (content, subject, label) => {
      if (chapters.length >= 10000) throw new Error('FB2 包含过多章节。');
      chapters.push({ body, content, subject, label, notes, auxiliary, file: `chapter-${chapters.length + 1}.xhtml` });
    };
    if (notes || !sections.length) {
      add(nodes, null, bodyTitle || (notes ? '注释' : auxiliary ? (body.getAttribute('name') || '附录') : '正文'));
      continue;
    }
    let pending = [];
    for (const node of nodes) {
      if (node.localName !== 'section') { pending.push(node); continue; }
      const label = titleText(elements(node, 'title')[0]) || (sections.length === 1 && bodyTitle) || `第 ${chapters.length + 1} 章`;
      add([...pending, node], node, label);
      pending = [];
    }
    chapters.at(-1).content.push(...pending);
  }
  if (!chapters.some(chapter => !chapter.auxiliary)) throw new Error('FB2 中找不到可转换的正文。');
  return chapters;
}

function buildTargets(chapters, signal) {
  const byNode = new WeakMap();
  const byId = new Map();
  let index = 0;
  function visit(node, file, depth) {
    if (depth > 64 || index > 150000) throw new Error('FB2 章节结构过于复杂。');
    signal?.throwIfAborted();
    const id = node.getAttribute('id') || node.getAttributeNS(xml, 'id');
    if (id || node.localName === 'section') {
      const anchor = `fb-${++index}`;
      const target = { file, anchor };
      byNode.set(node, target);
      if (id && !byId.has(id)) byId.set(id, target);
    }
    for (const child of elements(node)) visit(child, file, depth + 1);
  }
  for (const chapter of chapters) for (const node of chapter.content) visit(node, chapter.file, 0);
  return { byNode, byId };
}

function imageReader(book) {
  const binaries = new Map(elements(book, 'binary').map(node => [node.getAttribute('id'), node]));
  const images = new Map();
  let total = 0;
  function imageFor(node) {
    const id = fragment(href(node));
    if (!id || images.has(id)) return images.get(id);
    const binary = binaries.get(id);
    if (!binary) return undefined;
    const declared = (binary.getAttribute('content-type') || '').trim().toLowerCase();
    const type = declared === 'image/jpg' ? 'image/jpeg' : declared;
    const format = imageTypes.get(type);
    if (!format) return undefined;
    const raw = binary.textContent.replace(/\s/gu, '');
    if (raw.length < 8 || raw.length % 4 === 1 || !/^[a-z\d+/]+={0,2}$/i.test(raw)
        || raw.length > Math.ceil(maxImageBytes / 3) * 4) throw new Error('FB2 的内嵌图片过大或损坏。');
    const data = Buffer.from(raw, 'base64');
    total += data.length;
    if (data.length > maxImageBytes || total > maxSourceBytes || !format.header.test(data.subarray(0, 8).toString('hex'))) {
      throw new Error('FB2 的内嵌图片过大或损坏。');
    }
    const image = { file: `image-${images.size + 1}.${format.extension}`, id: `img-${images.size + 1}`, type, data };
    images.set(id, image);
    return image;
  }
  return { images, imageFor };
}

function renderNode(node, context, depth = 1) {
  if (node.nodeType === 3 || node.nodeType === 4) return escape(node.data);
  if (node.nodeType !== 1 || node.namespaceURI !== context.namespace || ignorable.has(node.localName)) return '';
  context.signal?.throwIfAborted();
  const name = node.localName;
  const target = context.byNode.get(node);
  const id = target ? ` id="${target.anchor}"` : '';
  const lang = languageTag(node.getAttributeNS(xml, 'lang'));
  const attrs = id + (lang ? ` xml:lang="${lang}"` : '');
  const render = (child, level = depth) => renderNode(child, context, level);
  const contents = () => Array.from(node.childNodes).map(child => render(child)).join('');
  if (name === 'section') {
    const body = Array.from(node.childNodes).map(child => render(child, depth + 1)).join('');
    return context.notes ? `<aside epub:type="footnote"${attrs}>${body}</aside>` : `<section${attrs}>${body}</section>`;
  }
  if (name === 'title' || name === 'subtitle') {
    const heading = Math.min(6, depth);
    const lines = elements(node, 'p');
    const content = name === 'title' && lines.length ? lines.map(line => {
      const text = Array.from(line.childNodes).map(child => render(child)).join('');
      const anchor = context.byNode.get(line)?.anchor;
      return anchor ? `<span id="${anchor}">${text}</span>` : text;
    }).join('<br/>') : contents();
    return `<h${heading}${attrs}>${content}</h${heading}>`;
  }
  if (name === 'image') {
    const image = context.imageFor(node);
    const alt = node.getAttribute('alt') || node.getAttribute('title') || '';
    return image ? `<img${attrs} src="${image.file}" alt="${escape(alt)}"/>` : alt || id ? `<span${attrs}>${escape(alt)}</span>` : '';
  }
  if (name === 'a') {
    const url = href(node);
    const link = context.byId.get(fragment(url));
    let resolved = link ? `${link.file}#${link.anchor}` : '';
    if (!resolved && /^(?:https?:\/\/|mailto:)/i.test(url) && URL.canParse(url)) resolved = url;
    const note = link && node.getAttribute('type') === 'note' ? ' epub:type="noteref"' : '';
    return resolved ? `<a${attrs}${note} href="${escape(resolved)}">${contents()}</a>` : `<span${attrs}>${contents()}</span>`;
  }
  if (name === 'empty-line') return `<br${attrs}/>`;
  const tag = tags.get(name);
  if (!tag) return id ? `<span${attrs}>${contents()}</span>` : contents();
  const tableWidths = ['td', 'th'].includes(tag) ? ['colspan', 'rowspan'].map(key => {
    const count = node.getAttribute(key);
    return /^[1-9]\d{0,2}$/.test(count) ? ` ${key}="${count}"` : '';
  }).join('') : '';
  return `<${tag}${attrs} class="fb-${name}"${tableWidths}>${contents()}</${tag}>`;
}

function chapterNav(chapter, byNode) {
  function nested(node) {
    const target = byNode.get(node);
    const label = titleText(elements(node, 'title')[0]);
    const sub = elements(node, 'section').map(nested).join('');
    if (!label || !target) return sub;
    return `<li><a href="${target.file}#${target.anchor}">${escape(label)}</a>${sub ? `<ol>${sub}</ol>` : ''}</li>`;
  }
  const children = chapter.subject && !chapter.notes ? elements(chapter.subject, 'section').map(nested).join('') : '';
  return `<li><a href="${chapter.file}">${escape(chapter.label)}</a>${children ? `<ol>${children}</ol>` : ''}</li>`;
}

function document(title, content, language) {
  return `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="${language}" lang="${language}"><head><title>${escape(title)}</title><link href="layout.css" rel="stylesheet" type="text/css"/></head><body>${content}</body></html>`;
}

function publication(root, { input, signal, title, author, language }) {
  const info = elements(elements(root, 'description')[0], 'title-info')[0];
  const bookTitle = value(elements(info, 'book-title')[0]) || title || path.parse(input).name;
  const creators = elements(info, 'author').map(node => ['first-name', 'middle-name', 'last-name'].map(part =>
    value(elements(node, part)[0])).filter(Boolean).join(' ') || value(elements(node, 'nickname')[0])).filter(Boolean);
  if (!creators.length && author) creators.push(author);
  const lang = languageTag(value(elements(info, 'lang')[0])) || languageTag(language) || 'und';
  const description = value(elements(info, 'annotation')[0], 5000);
  const chapters = scanChapters(root);
  const targets = buildTargets(chapters, signal);
  const { images, imageFor } = imageReader(root);
  const coverNode = elements(elements(info, 'coverpage')[0], 'image')[0];
  return { bookTitle, creators, lang, description, chapters, targets, images, imageFor, coverNode, namespace: root.namespaceURI };
}

export async function readFb2Publication(input, options = {}) {
  const root = await readBook(input, options.signal);
  return publication(root, { ...options, input });
}

async function writeBook(root, { input, output, signal, title, author, language }) {
  const { bookTitle, creators, lang, description, chapters, targets, images, imageFor, coverNode, namespace } =
    publication(root, { input, signal, title, author, language });
  const cover = coverNode && imageFor(coverNode);
  let textBytes = 0;
  const documents = chapters.map(chapter => {
    const context = { ...targets, imageFor, notes: chapter.notes, namespace, signal };
    const content = chapter.content.map(node => renderNode(node, context)).join('');
    const text = document(chapter.label, content, languageTag(chapter.body.getAttributeNS(xml, 'lang')) || lang);
    textBytes += Buffer.byteLength(text);
    if (textBytes > maxTextBytes) throw new Error('FB2 正文超过转换大小限制。');
    return Buffer.from(text);
  });
  const index = chapters.map(chapter => chapterNav(chapter, targets.byNode)).join('');
  const manifest = chapters.map((chapter, index) =>
    `<item id="c${index + 1}" href="${chapter.file}" media-type="application/xhtml+xml"/>`).join('');
  const spine = chapters.map((chapter, index) =>
    `<itemref idref="c${index + 1}"${chapter.auxiliary ? ' linear="no"' : ''}/>`).join('');
  const assets = [...images.values()].map(image =>
    `<item id="${image.id}" href="${image.file}" media-type="${image.type}"${image === cover ? ' properties="cover-image"' : ''}/>`).join('');
  const identifier = 'urn:uuid:' + randomUUID();
  const modified = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
  const metadata = `<dc:identifier id="id">${identifier}</dc:identifier><dc:title>${escape(bookTitle)}</dc:title>${creators.map(creator => `<dc:creator>${escape(creator)}</dc:creator>`).join('')}<dc:language>${lang}</dc:language>${description ? `<dc:description>${escape(description)}</dc:description>` : ''}<meta property="dcterms:modified">${modified}</meta>`;
  const zip = new yazl.ZipFile();
  zip.on('error', error => zip.outputStream.destroy(error));
  const add = (file, data, options) => zip.addBuffer(typeof data === 'string' ? Buffer.from(data) : data, file, { forceDosTimestamp: true, ...options });
  add('mimetype', 'application/epub+zip', { compress: false });
  add('META-INF/container.xml', '<?xml version="1.0" encoding="UTF-8"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>');
  add('OEBPS/book.opf', `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">${metadata}</metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="layout" href="layout.css" media-type="text/css"/>${cover ? '<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>' : ''}${manifest}${assets}</manifest><spine>${cover ? '<itemref idref="cover"/>' : ''}${spine}</spine></package>`);
  add('OEBPS/layout.css', css);
  add('OEBPS/nav.xhtml', document('目录', `<nav epub:type="toc"><h1>目录</h1><ol>${cover ? '<li><a href="cover.xhtml">封面</a></li>' : ''}${index}</ol></nav>`, lang));
  if (cover) add('OEBPS/cover.xhtml', document(bookTitle, `<section epub:type="cover"><img src="${cover.file}" alt="${escape(bookTitle)}"/></section>`, lang));
  chapters.forEach((chapter, index) => add('OEBPS/' + chapter.file, documents[index]));
  for (const image of images.values()) add('OEBPS/' + image.file, image.data, { compress: false });

  signal?.throwIfAborted();
  const file = await open(output, 'wx');
  let complete = false;
  try {
    const written = pipeline(zip.outputStream, file.createWriteStream(), { signal });
    zip.end();
    await written;
    complete = true;
  } finally {
    await file.close().catch(() => {});
    if (!complete) await unlink(output).catch(() => {});
  }
}

export async function convertFb2(options) {
  try {
    const root = await readBook(options.input, options.signal);
    await writeBook(root, options);
  } catch (error) {
    options.signal?.throwIfAborted();
    if (error.code === 'EEXIST') throw new Error('转换目标文件已存在。', { cause: error });
    const detail = /^(?:FB2\s+)?[\u3400-\u9fff]/u.test(error.message) ? error.message : '请检查文件内容或目标目录权限。';
    throw new Error(`无法转换 FB2。${detail}`, { cause: error });
  }
}
