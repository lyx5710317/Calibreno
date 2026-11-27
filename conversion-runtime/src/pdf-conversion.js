import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { extractPdfPages } from './convert-pdf.js';
import { writeEpub } from './convert-ebook.js';
import { createPdfWorkspaces } from './pdf-workspace.js';

const id = 'document';

export async function convertPdfWithCheckpoints(options) {
  const { input, output, workspace, pages, title, author, language, signal } = options;
  const ocr = Boolean(options.ocr);
  const layout = options.sourceLayout || 'horizontal';
  const pdfLanguage = options.pdfLanguage || 'auto';
  const defaults = { layout, language: pdfLanguage, ocr };
  const store = createPdfWorkspaces(workspace);
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(input, { signal })) hash.update(chunk);
  const digest = hash.digest('hex');
  let info = await store.metadata(id);
  if (info && info.sourceDigest !== digest) {
    throw new Error('此工作区属于另一份 PDF，请选择对应的原件或新建工作区。');
  }
  const requested = pages ?? (info ? await store.missingPages(id, info, signal) : undefined);
  const failedPages = [];
  let retainedPrevious = false;
  let previewDirectory;
  if (pages) {
    previewDirectory = path.join(store.directory(id), 'previews');
    await mkdir(previewDirectory, { recursive: true });
  }
  if (!requested || requested.length > 0) {
    await extractPdfPages({
      ...options, ocr, layout, pdfLanguage, pages: requested, previewDirectory,
      continueOnPageError: true,
      async onPage(frame) {
        if (!info) {
          await store.initialize(id, digest, frame.total, { author, language });
          info = await store.metadata(id);
        }
        if (frame.total !== info.total) throw new Error('PDF 页数与工作区记录不一致。');
        const saved = await store.savePage(id, frame, defaults);
        if (pages && frame.warning === 'PAGE_READ') {
          failedPages.push(frame.page);
          if (!saved) retainedPrevious = true;
        }
      },
    });
  }
  if (pages) {
    if (failedPages.length > 0) {
      const previous = retainedPrevious ? '已保留此前可用的识别结果。' : '';
      throw new Error(`PDF 第 ${failedPages.join('、')} 页重跑失败。${previous}`);
    }
    return store.overview(id, { includeReview: true });
  }
  const records = await store.loadPages(id, info, signal);
  const unreadable = records.find(frame => frame.warning === 'PAGE_READ');
  if (unreadable) throw new Error(`PDF 第 ${unreadable.page} 页无法识别，请重跑此页后再生成 EPUB。`);
  const sections = records.filter(frame => frame.text).map(frame => ({ text: frame.text, label: `第 ${frame.page} 页` }));
  if (!sections.length) throw new Error('没有识别到可转换的文字，请检查扫描件。');
  await writeEpub(output, sections, { title, author, language: pdfLanguage === 'auto' ? language : pdfLanguage, signal });
  return { total: records.length };
}
