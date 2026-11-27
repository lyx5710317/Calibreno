import { lstat, mkdir, readFile, readdir, unlink } from 'node:fs/promises';
import path from 'node:path';
import { maxPdfPages, pdfLanguages, pdfLayouts, validPageDetails } from './convert-pdf.js';
import { atomicJson } from './utils.js';

const maxPageBytes = 12 * 1024 * 1024;
const maxWorkspaceText = 128 * 1024 * 1024;
const revision = 1;

function needsReview(record) {
  return !record || record.warning === 'PAGE_READ' || Boolean(record.lowConfidence?.length) || record.ai === 'fallback';
}

async function readRecord(file, limit) {
  try {
    const info = await lstat(file);
    if (!info.isFile() || info.size > limit) throw new Error('PDF 工作区记录大小或类型无效。');
    return JSON.parse(await readFile(file, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    if (error instanceof SyntaxError) throw new Error('PDF 工作区记录无法读取。');
    throw error;
  }
}

export function createPdfWorkspaces(dataDir) {
  const root = path.join(dataDir, 'pdf-pages');
  const directory = id => path.join(root, id);
  const index = id => path.join(directory(id), 'document.json');
  const pagePath = (id, page) => path.join(directory(id), `page-${page}.json`);
  const reviewCache = new Map();

  function markReview(id, page, review) {
    void reviewCache.get(id)?.then(flags => { if (review) flags.add(page); else flags.delete(page); });
  }

  async function metadata(id) {
    const record = await readRecord(index(id), 4096);
    if (!record) return null;
    if (record.version !== revision || !Number.isInteger(record.total) || record.total < 1 || record.total > maxPdfPages
        || !/^[a-f0-9]{64}$/u.test(record.sourceDigest)) throw new Error('PDF 工作区索引无效。');
    return record;
  }

  async function initialize(id, sourceDigest, total, { author = '', language = '' } = {}) {
    await mkdir(directory(id), { recursive: true });
    await atomicJson(index(id), { version: revision, sourceDigest, total, author: author.slice(0, 500), language: language.slice(0, 80) });
    reviewCache.delete(id);
  }

  async function loadPage(id, page, total) {
    const record = await readRecord(pagePath(id, page), maxPageBytes);
    if (!record) return null;
    if (!validPageDetails(record) || record.page !== page || record.total !== total || typeof record.rawText !== 'string'
        || typeof record.forceOcr !== 'boolean' || !pdfLayouts.has(record.layout) || !pdfLanguages.has(record.language)) {
      throw new Error(`PDF 第 ${page} 页的工作区记录无效，请重跑此页。`);
    }
    return record;
  }

  async function savePage(id, frame, { layout, language, ocr }, rawText = frame.text, updatedBy) {
    const { page, total, text, ocr: usedOcr, lowConfidence = [], warning, ai, aiFallback } = frame;
    if (warning === 'PAGE_READ') {
      const previous = await loadPage(id, page, total).catch(() => null);
      if (previous && previous.warning !== 'PAGE_READ') return false;
    }
    const value = { page, total, rawText, text, ocr: usedOcr, layout, language, forceOcr: ocr, lowConfidence,
      ...(warning && { warning }), ...(ai && { ai }), ...(aiFallback && { aiFallback }), ...(updatedBy && { updatedBy }) };
    await atomicJson(pagePath(id, page), value);
    markReview(id, page, needsReview(value));
    return true;
  }

  async function scanReview(id, total, saved) {
    const flags = new Set();
    for (let offset = 0; offset < saved.length; offset += 8) {
      await Promise.all(saved.slice(offset, offset + 8).map(async page => {
        const record = await loadPage(id, page, total).catch(() => null);
        if (needsReview(record)) flags.add(page);
      }));
    }
    return flags;
  }

  async function overview(id, { includeReview = false } = {}) {
    const info = await metadata(id);
    if (!info) return null;
    const files = await readdir(directory(id), { withFileTypes: true });
    const saved = files.flatMap(item => {
      const number = item.isFile() && Number(/^page-([1-9]\d*)\.json$/u.exec(item.name)?.[1]);
      return number && number <= info.total ? [number] : [];
    }).sort((a, b) => a - b);
    if (!includeReview) return { ...info, saved };
    let pending = reviewCache.get(id);
    if (!pending) {
      pending = scanReview(id, info.total, saved);
      if (reviewCache.size >= 32) reviewCache.delete(reviewCache.keys().next().value);
      reviewCache.set(id, pending);
    }
    const flags = await pending;
    const filesByPage = new Set(saved);
    const reviewPages = Array.from({ length: info.total }, (_, index) => index + 1)
      .filter(page => !filesByPage.has(page) || flags.has(page));
    return { ...info, saved, reviewPages };
  }

  async function loadPages(id, info, signal) {
    const pages = [];
    let bytes = 0;
    for (let page = 1; page <= info.total; page++) {
      signal.throwIfAborted();
      const record = await loadPage(id, page, info.total);
      if (!record) throw new Error(`PDF 第 ${page} 页还没有识别结果，重跑此页后再导出。`);
      bytes += Buffer.byteLength(record.rawText) + Buffer.byteLength(record.text);
      if (bytes > maxWorkspaceText) throw new Error('PDF 的逐页文字超过导出大小限制，请拆分原文件。');
      pages.push(record);
    }
    return pages;
  }

  async function missingPages(id, info, signal) {
    const missing = [];
    let bytes = 0;
    for (let page = 1; page <= info.total; page++) {
      signal.throwIfAborted();
      const record = await loadPage(id, page, info.total).catch(() => null);
      if (!record || record.warning === 'PAGE_READ') {
        missing.push(page);
        continue;
      }
      bytes += Buffer.byteLength(record.rawText) + Buffer.byteLength(record.text);
      if (bytes > maxWorkspaceText) throw new Error('PDF 的逐页文字超过导出大小限制，请拆分原文件。');
    }
    return missing;
  }

  async function discardPages(id, pages) {
    for (const page of pages) {
      await unlink(pagePath(id, page)).catch(error => { if (error.code !== 'ENOENT') throw error; });
      markReview(id, page, true);
    }
  }

  async function invalidate(id) {
    reviewCache.delete(id);
    await unlink(index(id));
  }

  return { directory, initialize, metadata, loadPage, savePage, overview, loadPages, missingPages, discardPages, invalidate };
}
