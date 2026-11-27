import { spawn } from 'node:child_process';
import { createReadStream, createWriteStream } from 'node:fs';
import { lstat, mkdtemp, open, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { writeKindleEpub } from './kindle-epub.js';

export const kindleUnavailable = '缺少内置 MOBI／AZW3 转换组件，请检查 Windows 安装。';
const inputTooLarge = 'MOBI／AZW3 原件超过 256 MB，无法转换。';
const unsupported = '无法读取 MOBI／AZW3 原件，请确认文件完整且格式受支持。';
const protectedBook = '此 MOBI／AZW3 包含 DRM 或加密，无法转换。';
const noEpub = '无法从该 MOBI／AZW3 重建 EPUB，文件可能损坏或使用不支持的版式。';
const maxInput = 256 * 1024 * 1024;

export async function kindleWorkerAvailable(workerPath) {
  if (!workerPath || !path.isAbsolute(workerPath)) return false;
  const file = await stat(workerPath).catch(() => null);
  return Boolean(file?.isFile() && file.size);
}

async function kindleHeader(input) {
  const file = await open(input, 'r');
  try {
    const info = await file.stat();
    if (!info.isFile() || info.size < 120) throw new Error(unsupported);
    if (info.size > maxInput) throw new Error(inputTooLarge);
    const header = Buffer.alloc(82);
    if ((await file.read(header, 0, header.length, 0)).bytesRead !== header.length
        || header.toString('ascii', 60, 68) !== 'BOOKMOBI') throw new Error(unsupported);
    const records = header.readUInt16BE(76);
    const start = header.readUInt32BE(78);
    const record = Buffer.alloc(40);
    if (!records || start < 78 + 8 * records || start + record.length > info.size
        || (await file.read(record, 0, record.length, start)).bytesRead !== record.length
        || record.toString('ascii', 16, 20) !== 'MOBI') throw new Error(unsupported);
    if (record.readUInt16BE(12)) throw new Error(protectedBook);
  } finally { await file.close(); }
}

function runWorker(executable, cwd, signal) {
  signal?.throwIfAborted();
  return new Promise((resolve, reject) => {
    // Relative names support narrow-path libmobi builds.
    const args = ['-e', 'book.mobi'];
    const child = spawn(executable, args, { cwd, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let log = '';
    let processError, timedOut = false;
    const absorb = chunk => { log = (log + chunk.toString('utf8')).slice(-8192); };
    child.stdout.on('data', absorb);
    child.stderr.on('data', absorb);
    child.once('error', error => { processError = error; });
    const abort = () => child.kill();
    const timer = setTimeout(() => { timedOut = true; abort(); }, 180_000);
    timer.unref();
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
    child.once('close', code => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      if (signal?.aborted) reject(signal.reason);
      else if (timedOut) reject(new Error('MOBI／AZW3 离线转换超时。'));
      else if (processError) reject(new Error(kindleUnavailable, { cause: processError }));
      else if (code !== 0 && /\((?:encrypted file|unsupported encryption)\)/i.test(log)) reject(new Error(protectedBook));
      else if (code === 0) resolve();
      else reject(new Error(noEpub));
    });
  });
}

export async function convertKindleToEpub({ input, output, workerPath, signal, title, author, language }) {
  signal?.throwIfAborted();
  if (!await kindleWorkerAvailable(workerPath)) throw new Error(kindleUnavailable);
  let work;
  try {
    await kindleHeader(input);
    work = await mkdtemp(path.join(tmpdir(), 'calibreno-kindle-'));
    const staged = path.join(work, 'book.mobi');
    const result = path.join(work, 'book.epub');
    await pipeline(createReadStream(input, { start: 0, end: maxInput }), createWriteStream(staged, { flags: 'wx' }), { signal });
    await kindleHeader(staged);
    await runWorker(workerPath, work, signal);
    const file = await lstat(result).catch(() => null);
    if (!file?.isFile() || !file.size) throw new Error(noEpub);
    await writeKindleEpub(result, output, { signal, title, author, language });
  } catch (error) {
    signal?.throwIfAborted();
    if (/^[\p{Script=Han}]/u.test(error.message)) throw error;
    throw new Error(noEpub, { cause: error });
  } finally {
    if (work) await rm(work, { force: true, recursive: true, maxRetries: 4, retryDelay: 100 }).catch(() => {});
  }
}
