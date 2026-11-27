import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { mkdir, writeFile, rename, unlink, lstat } from 'node:fs/promises';

export async function atomicJson(file, value, { backup = false, mode } = {}) {
  await mkdir(path.dirname(file), { recursive: true });
  const temp = `${file}.${randomUUID()}.tmp`;
  try {
    // Finish and flush the new file before touching either committed generation.
    await writeFile(temp, JSON.stringify(value, null, 2), { flag: 'wx', flush: true, mode });
    if (backup) {
      try {
        if (!(await lstat(file)).isFile()) throw Object.assign(new Error('JSON 数据路径不是普通文件'), { code: 'EISDIR' });
        await rename(file, file + '.bak');
      }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
    // A crash between the two renames leaves the previous generation in .bak.
    await rename(temp, file);
  } finally { await unlink(temp).catch(error => { if (error.code !== 'ENOENT') throw error; }); }
}
