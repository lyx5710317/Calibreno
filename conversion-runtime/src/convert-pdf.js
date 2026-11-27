import { spawn } from 'node:child_process';
import { stat } from 'node:fs/promises';
import path from 'node:path';
import { StringDecoder } from 'node:string_decoder';

export const pdfUnavailable = '缺少内置 PDF 识别组件，请检查 Windows 安装。';
export const maxPdfTextBytes = 64 * 1024 * 1024;
const maxFrame = 4 * 1024 * 1024;
export const maxPdfPages = 3000;
export const maxPreviewPages = 20;
export const pdfLayouts = new Set(['horizontal', 'vertical-rtl']);
export const pdfLanguages = new Set(['auto', 'zh-Hant', 'ja']);
const pageWarnings = new Set(['EMPTY', 'PAGE_READ']);
const messages = {
  PDF_OPEN: '无法读取 PDF，文件可能已损坏或受密码保护。',
  PAGE_READ: 'PDF 有页面无法读取或识别，转换未完成。',
  OCR_RUNTIME: '内置 OCR 组件或模型无法加载，请检查应用安装。',
  PAGE_LIMIT: 'PDF 超过 3000 页，请拆分后转换。',
  PAGE_RANGE: '指定页码超出 PDF 的实际页数，请重新填写。',
};

export class PdfWorkerError extends Error {}

function validPdfPages(value) {
  return Array.isArray(value) && value.length > 0 && value.length <= maxPdfPages
    && value.every((page, index) => Number.isInteger(page) && page > (value[index - 1] ?? 0) && page <= maxPdfPages);
}

export function validPreviewPages(value) {
  return validPdfPages(value) && value.length <= maxPreviewPages;
}

export function validPageDetails(frame) {
  return frame && Number.isInteger(frame.page) && frame.page >= 1
    && Number.isInteger(frame.total) && frame.total >= frame.page && frame.total <= maxPdfPages
    && typeof frame.text === 'string' && typeof frame.ocr === 'boolean'
    && (frame.warning === undefined || pageWarnings.has(frame.warning))
    && (frame.image === undefined || typeof frame.image === 'boolean')
    && (frame.ai === undefined || frame.ai === 'checked' || frame.ai === 'fallback')
    && (frame.aiFallback === undefined || (frame.ai === 'fallback' && ['PROVIDER', 'RESPONSE'].includes(frame.aiFallback)))
    && (frame.lowConfidence === undefined || (Array.isArray(frame.lowConfidence) && frame.lowConfidence.length <= 25
      && frame.lowConfidence.every(item => typeof item === 'string' && item.length <= 200)));
}

export async function pdfWorkerAvailable(executable) {
  if (typeof executable !== 'string' || !path.isAbsolute(executable)) return false;
  const [worker, ...models] = await Promise.all([executable, ...[
    'PP-OCRv6_det_small.onnx', 'PP-OCRv6_rec_small.onnx', 'ch_ppocr_mobile_v2.0_cls_mobile.onnx',
  ].map(model => path.join(path.dirname(executable), '_internal', 'rapidocr', 'models', model))].map(file => stat(file).catch(() => null)));
  return Boolean(worker?.isFile() && worker.size && models.every(model => model?.isFile() && model.size));
}

async function readPdf({ executable, input, signal = new AbortController().signal, ocr, onProgress, layout, language,
  pages, previewDirectory, continueOnPageError, onPage }) {
  signal.throwIfAborted();
  if (pages !== undefined && !validPdfPages(pages)) throw new Error(messages.PAGE_RANGE);
  if (previewDirectory !== undefined && (typeof previewDirectory !== 'string' || !path.isAbsolute(previewDirectory)
      || !validPreviewPages(pages))) throw new Error('无效的 PDF 原页预览目录或页码。');
  const args = [input, ocr ? 'ocr' : 'auto'];
  if (layout) args.push('--layout', layout);
  if (language) args.push('--language', language);
  if (pages) args.push('--pages', pages.join(','));
  if (previewDirectory) args.push('--preview-dir', previewDirectory);
  if (continueOnPageError) args.push('--continue-on-page-error');
  const child = spawn(executable, args, {
    cwd: path.dirname(executable), windowsHide: true, stdio: ['ignore', 'pipe', 'ignore'],
  });
  const allPages = [];
  const textLimit = previewDirectory ? 8 * 1024 * 1024 : maxPdfTextBytes;
  let total = 0, textSize = 0, workerError;
  let buffer = '';
  const decoder = new StringDecoder('utf8');
  const completed = new Promise(resolve => child.once('close', resolve));
  child.once('error', () => { workerError ??= new PdfWorkerError('无法启动内置 PDF 识别组件，请检查应用安装。'); });
  const invalidFrame = () => new PdfWorkerError('内置 PDF 组件返回了无法读取的页面结果。');

  async function accept(line) {
    let frame;
    try { frame = JSON.parse(line); } catch { throw invalidFrame(); }
    if (Object.hasOwn(messages, frame?.error)) throw new PdfWorkerError(messages[frame.error]);
    const processed = allPages.length;
    if (!validPageDetails(frame) || (total && total !== frame.total)
        || frame.page !== (pages ? pages[processed] : processed + 1)) throw invalidFrame();
    total = frame.total;
    textSize += Buffer.byteLength(frame.text);
    if (textSize > textLimit) {
      throw new PdfWorkerError(previewDirectory ? '试转文字过多，请缩小页码范围。' : 'PDF 提取的文字超过 64 MB，请拆分后转换。');
    }
    const text = frame.text.replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\ufffe\uffff]/gu, '').trim();
    const { page, ocr, lowConfidence = [], image, warning } = frame;
    const record = { page, total, text, ocr, lowConfidence, ...(image && { image }), ...(warning && { warning }) };
    allPages.push(record);
    // Pause pipe consumption while the caller writes an ordered page checkpoint.
    await onPage?.(record);
    signal.throwIfAborted();
    if (workerError) throw workerError;
    onProgress?.({ page: allPages.length, total: pages?.length ?? total });
  }
  const abort = () => child.kill();
  signal.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(() => {
    workerError ??= new PdfWorkerError('PDF 转换超过 30 分钟，请拆分后重试。');
    child.kill();
  }, 30 * 60 * 1000);
  try {
    for await (const chunk of child.stdout) {
      signal.throwIfAborted();
      if (workerError) throw workerError;
      buffer += decoder.write(chunk);
      for (let end; (end = buffer.indexOf('\n')) !== -1;) {
        const line = buffer.slice(0, end);
        if (Buffer.byteLength(line) > maxFrame) throw invalidFrame();
        buffer = buffer.slice(end + 1);
        await accept(line);
      }
      if (Buffer.byteLength(buffer) > maxFrame) throw invalidFrame();
    }
    const code = await completed;
    signal.throwIfAborted();
    if (workerError) throw workerError;
    if (code !== 0 || !total || allPages.length !== (pages?.length ?? total) || buffer || decoder.end()) {
      throw new PdfWorkerError('内置 PDF 组件未能完成所有页面，请检查文件或应用安装。');
    }
    return allPages;
  } catch (error) {
    child.kill();
    await completed;
    signal.throwIfAborted();
    if (workerError) throw workerError;
    throw error;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

export async function extractPdfPages({ pdfLanguage, ...options }) {
  if (!await pdfWorkerAvailable(options.executable)) throw new PdfWorkerError(pdfUnavailable);
  return readPdf({ ...options, language: pdfLanguage });
}
