import { execFile } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { copyFile, mkdir, readFile, rename, stat, unlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const run = promisify(execFile);
const root = fileURLToPath(new URL('../.local/kindle-runtime/', import.meta.url));
const output = path.join(root, 'dist', 'kindle-runtime');
const executable = path.join(output, 'mobitool.exe');
const source = {
  filename: 'libmobi-0.12.tar.gz', folder: 'libmobi-0.12', bytes: 2_653_654,
  sha256: '9a6fb2c56b916f8fa8b15e0c71008d908109508c944ea1d297881d4e277bf7e7',
  url: 'https://github.com/bfabiszewski/libmobi/releases/download/v0.12/libmobi-0.12.tar.gz',
};
const toolchain = {
  filename: 'zig-x86_64-windows-0.14.1.zip', folder: 'zig-x86_64-windows-0.14.1', bytes: 82_229_343,
  sha256: '554f5378228923ffd558eac35e21af020c73789d87afeabf4bfd16f2e6feed2c',
  url: 'https://ziglang.org/download/0.14.1/zig-x86_64-windows-0.14.1.zip',
};

async function digest(file) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}

async function verified(file, asset) {
  return (await stat(file).catch(() => null))?.size === asset.bytes && await digest(file) === asset.sha256;
}

async function fetchArchive(asset) {
  const file = path.join(root, asset.filename);
  if (await verified(file, asset)) return file;
  const temp = file + '.' + randomUUID() + '.part';
  let received = 0;
  const response = await fetch(asset.url, { signal: AbortSignal.timeout(540_000) });
  if (!response.ok || !response.body) throw new Error(`Could not download ${asset.filename}.`);
  try {
    await pipeline(Readable.fromWeb(response.body), new Transform({
      transform(chunk, _encoding, done) {
        received += chunk.length;
        done(received > asset.bytes ? new Error('Unexpected build archive size.') : null, chunk);
      },
    }), createWriteStream(temp, { flags: 'wx' }));
    if (!await verified(temp, asset)) throw new Error(`${asset.filename} failed SHA-256 verification.`);
    await rename(temp, file);
    return file;
  } finally {
    await unlink(temp).catch(() => {});
  }
}

async function build() {
  if (process.platform !== 'win32' || process.arch !== 'x64') throw new Error('The Kindle runtime build requires Windows x64.');
  const sourceCopy = path.join(output, source.filename);
  const buildHash = await digest(fileURLToPath(import.meta.url));
  const stamp = path.join(root, 'build.json');
  const previous = await readFile(stamp, 'utf8').then(JSON.parse).catch(() => null);
  if (previous?.build === buildHash && await verified(sourceCopy, source)
      && (await stat(path.join(output, 'COPYING-LGPL.txt')).catch(() => null))?.size
      && (await stat(path.join(output, 'COPYING-GPL.txt')).catch(() => null))?.size
      && await digest(executable).catch(() => null) === previous.executable) return;

  await mkdir(output, { recursive: true });
  const archives = await Promise.all([fetchArchive(source), fetchArchive(toolchain)]);
  for (const archive of archives) await run('tar.exe', ['-xf', archive, '-C', root], { windowsHide: true, timeout: 120_000 });
  const sourceDir = path.join(root, source.folder);
  const compiled = path.join(root, 'mobitool.exe');
  const units = [
    ...['buffer', 'compression', 'debug', 'index', 'memory', 'meta', 'miniz', 'opf', 'parse_rawml', 'read', 'structure', 'util', 'write', 'xmlwriter']
      .map(name => `src/${name}.c`),
    'tools/common.c', 'tools/mobitool.c', 'tools/win32/getopt.c',
  ];
  // Build a separate executable from unmodified libmobi. The encryption module is omitted.
  await run(path.join(root, toolchain.folder, 'zig.exe'), [
    'cc', '-target', 'x86_64-windows-gnu', '-static', '-O2', '-s', '-std=gnu99', '-Isrc', '-Wno-date-time', '-Wno-macro-redefined',
    '-DUSE_MINIZ', '-DUSE_XMLWRITER', '-DMINIZ_NO_ZLIB_COMPATIBLE_NAMES', '-DPACKAGE_VERSION="0.12"',
    ...units, '-o', compiled,
  ], { cwd: sourceDir, windowsHide: true, timeout: 240_000 });
  await run(compiled, ['-v'], { windowsHide: true, timeout: 10_000 });
  await Promise.all([
    copyFile(compiled, executable),
    copyFile(archives[0], sourceCopy),
    copyFile(path.join(sourceDir, 'COPYING'), path.join(output, 'COPYING-LGPL.txt')),
    copyFile(new URL('../../LICENSE', import.meta.url), path.join(output, 'COPYING-GPL.txt')),
  ]);
  await writeFile(stamp, JSON.stringify({ build: buildHash, executable: await digest(executable) }));
}

await build();
