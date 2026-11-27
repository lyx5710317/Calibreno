import { spawnSync } from 'node:child_process';
import { cp, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { setTimeout } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const defaultDestination = path.resolve(root, '../cmd/dalibre/resources/windows-amd64/book-conversion');
const [destination = defaultDestination, existingRuntime] = process.argv.slice(2);
if (process.platform !== 'win32' || process.arch !== 'x64') throw new Error('The native conversion runtime requires Windows x64.');
if (process.argv.length > 4) throw new Error('Usage: node scripts/stage.mjs [destination [existing ZlibraryNo .local directory]]');

const native = existingRuntime ? path.resolve(existingRuntime) : path.join(root, '.local');
const out = path.resolve(destination);
const parent = path.dirname(out);
if (out === parent) throw new Error('Choose a directory below the filesystem root for the conversion runtime.');
const outRelativeToSource = path.relative(root, out);
if (!path.isAbsolute(outRelativeToSource) && !outRelativeToSource.startsWith('..' + path.sep)) {
  throw new Error('Stage the conversion runtime outside the conversion-runtime source directory.');
}

function build(command, args) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', windowsHide: true });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} returned ${result.status}`);
}

async function moveDirectory(source, target) {
  for (let attempt = 0; ; attempt++) {
    try {
      await rename(source, target);
      return;
    } catch (error) {
      // Windows can briefly lock freshly copied executables for scanning.
      if (attempt === 12 || !['EACCES', 'EBUSY', 'EPERM'].includes(error.code)) throw error;
      await setTimeout(100 * (attempt + 1));
    }
  }
}

const electron = path.join(existingRuntime ? path.dirname(native) : root, 'node_modules', 'electron');
const paths = [
  [path.join(electron, 'dist'), 'electron'],
  [path.join(native, 'pdf-runtime', 'dist', 'pdf-ocr'), 'pdf-ocr'],
  [path.join(native, 'kindle-runtime', 'dist', 'kindle-runtime'), 'kindle-runtime'],
];

async function managedDirectory(dir) {
  const info = await lstat(dir).catch(error => {
    if (error.code !== 'ENOENT') throw error;
    return null;
  });
  if (!info) return false;
  const children = info.isDirectory() && !info.isSymbolicLink() ? await readdir(dir) : [];
  const allowed = ['engine', ...paths.map(([, name]) => name)];
  const manifest = await readFile(path.join(dir, 'engine', 'package.json'), 'utf8').then(JSON.parse).catch(() => null);
  if (!allowed.every(name => children.includes(name)) || children.some(name => !allowed.includes(name))
      || manifest?.name !== 'calibreno-conversion') {
    throw new Error(`The output path already exists and is not a staged Calibreno runtime: ${dir}`);
  }
  return true;
}

const replace = await managedDirectory(out);
if (!existingRuntime) {
  const python = process.env.CALIBRENO_BUILD_PYTHON;
  build(python || 'py', python ? ['scripts/build-pdf-runtime.py'] : ['-3.11', 'scripts/build-pdf-runtime.py']);
  build(process.execPath, ['scripts/build-kindle-runtime.mjs']);
  build(process.execPath, [path.join(electron, 'install.js')]);
}
const lock = JSON.parse(await readFile(path.join(root, 'package-lock.json'), 'utf8'));
const expectedElectron = lock.packages['node_modules/electron'].version;
const actualElectron = (await readFile(path.join(electron, 'dist', 'version'), 'utf8')).trim();
if (actualElectron !== expectedElectron) throw new Error(`Expected Electron ${expectedElectron}, found ${actualElectron}.`);

const directories = Object.fromEntries(paths.map(([source, target]) => [target, source]));
for (const [target, filename] of [
  ['electron', 'electron.exe'], ['electron', 'resources.pak'], ['electron', 'LICENSES.chromium.html'],
  ['pdf-ocr', 'pdf-ocr.exe'], ['pdf-ocr', 'licenses/Python.txt'],
  ['kindle-runtime', 'mobitool.exe'], ['kindle-runtime', 'libmobi-0.12.tar.gz'],
  ['kindle-runtime', 'COPYING-GPL.txt'], ['kindle-runtime', 'COPYING-LGPL.txt'],
]) {
  const file = path.join(directories[target], filename);
  const info = await stat(file).catch(() => null);
  if (!info?.isFile() || !info.size) throw new Error(`Missing runtime file: ${file}`);
}
build(path.join(directories['pdf-ocr'], 'pdf-ocr.exe'), ['--check-runtime']);
build(path.join(directories['kindle-runtime'], 'mobitool.exe'), ['-v']);

await mkdir(parent, { recursive: true });
// Keep the normal build scratch area outside MyGo's resources directory.
const scratchParent = out === defaultDestination ? path.join(root, '.local') : parent;
await mkdir(scratchParent, { recursive: true });
const realParent = await realpath(scratchParent);
const scratch = await mkdtemp(path.join(realParent, '.calibreno-conversion-'));
const staged = path.join(scratch, 'new');
const backup = path.join(scratch, 'previous');
let preserveScratch = false;
try {
  await mkdir(staged);
  for (const [source, target] of paths) {
    await cp(source, path.join(staged, target), { recursive: true });
  }
  const engine = path.join(staged, 'engine');
  await mkdir(engine);
  for (const file of ['LICENSE', 'package.json', 'main.js', 'src', 'desktop']) {
    await cp(path.join(root, file), path.join(engine, file), { recursive: true });
  }
  for (const [module, entry] of Object.entries(lock.packages)) {
    if (!module || entry.dev) continue;
    await cp(path.join(root, module), path.join(engine, module), { recursive: true });
  }
  if (replace) await moveDirectory(out, backup);
  try {
    await moveDirectory(staged, out);
  } catch (error) {
    if (replace) {
      try {
        await moveDirectory(backup, out);
      } catch (rollbackError) {
        preserveScratch = true;
        throw new AggregateError([error, rollbackError], `Previous runtime is at ${backup}`);
      }
    }
    throw error;
  }
} finally {
  if (!preserveScratch && path.dirname(await realpath(scratch)) === realParent) {
    await rm(scratch, { recursive: true, force: true, maxRetries: 3 });
  }
}
console.log(out);
