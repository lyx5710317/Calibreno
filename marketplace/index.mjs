// Writes marketplace.json, the index Calibreno's Marketplace reads, from
// the approved plugins in catalog.json (packed into the marketplace branch's
// packages/ as <id>-<version>.dalibre-plugin) and the community's
// (community.json: repositories, each at its latest release).
//
//   node index.mjs [--out FILE]   write marketplace.json (here by default)
//   node index.mjs --check        validate the complete index without writing it
//   node index.mjs --folders      list approved local plugin folders, one per line
//
// GITHUB_REPOSITORY selects the publishing repository. GITHUB_TOKEN raises
// GitHub's rate limit. DALIBRE_PLUGIN points to the dalibre-plugin binary
// used to validate each community plugin.

import { readFile, writeFile, mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { execFileSync } from "node:child_process";
import { setTimeout } from "node:timers/promises";
import { unzipSync, strFromU8 } from "fflate";

const repo = process.env.GITHUB_REPOSITORY ?? "lyx5710317/Calibreno";
if (!/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\/[a-z0-9_.-]+$/i.test(repo) || /\/\.{1,2}$/.test(repo)) {
  throw new Error("GITHUB_REPOSITORY must be a GitHub owner/repository");
}
const packages = `https://raw.githubusercontent.com/${repo}/refs/heads/marketplace/packages`;
const outIndex = process.argv.indexOf("--out");
const out = outIndex > 0 ? process.argv[outIndex + 1] : "marketplace.json";
const checkOnly = process.argv.includes("--check");
const foldersOnly = process.argv.includes("--folders");
const token = process.env.GITHUB_TOKEN;
const reserved = (id) => id === "dalibre" || id.startsWith("dalibre.");
const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/;

const catalog = JSON.parse(await readFile("catalog.json", "utf8"));
const categories = new Set(catalog.categories.map((c) => c.id));
const localEntries = Object.entries(catalog.plugins);
for (const [folder, entry] of localEntries) {
  if (!/^[a-z0-9][a-z0-9-]*$/.test(folder) || !categories.has(entry.category) ||
      (entry.origin !== "upstream" && entry.origin !== "calibreno")) {
    throw new Error(`Invalid catalog entry for ${folder}: check folder, category and origin`);
  }
}
if (foldersOnly) {
  for (const [folder] of localEntries) console.log(`plugins/${folder}`);
  process.exit(0);
}
const community = JSON.parse(await readFile("community.json", "utf8"));
const downloads = checkOnly ? new Map() : await calibreDownloads();

/** The listing of a plugin, from its manifest. */
function listing(m, extra) {
  return {
    id: m.id,
    name: m.name,
    version: m.version,
    description: m.description,
    author: m.author,
    license: m.license,
    icon: m.icon,
    homepage: m.homepage,
    dalibre: m.dalibre,
    category: extra.category,
    origin: extra.origin,
    calibre: extra.calibre,
    downloads: extra.calibre ? downloads.get(extra.calibre.toLowerCase()) : undefined,
    repo: extra.repo,
    community: extra.community || undefined,
    package: extra.package,
    capabilities: m.capabilities,
    permissions: m.permissions ?? { hosts: [] },
  };
}

const plugins = [];
const seen = new Set();
function add(l) {
  if (seen.has(l.id)) throw new Error(`id ${l.id} is already listed`);
  seen.add(l.id);
  plugins.push(l);
}

for (const [folder, entry] of localEntries) {
  const m = JSON.parse(await readFile(`plugins/${folder}/plugin.json`, "utf8"));
  validateManifest(m);
  add(listing(m, { ...entry, repo, package: `${packages}/${m.id}-${m.version}.dalibre-plugin` }));
}

const errors = [];
for (const entry of community.plugins) {
  try {
    const l = await resolveCommunity(entry);
    add(l);
    console.log(`${entry.repo}: ${l.name} ${l.version}`);
  } catch (err) {
    errors.push(`${entry.repo}: ${err.message}`);
  }
}

for (const e of errors) console.error(`✗ ${e}`);
if (errors.length) process.exit(1);
const featured = catalog.featured ?? [];
if (!Array.isArray(featured) || featured.some((id) => !seen.has(id)) || new Set(featured).size !== featured.length) {
  throw new Error("Featured plugin IDs must be unique and present in the catalog");
}
if (checkOnly) process.exit(0);

await writeFile(
  out,
  JSON.stringify({ name: catalog.name, categories: catalog.categories, featured, plugins }, null, 2) + "\n",
);
console.log(`marketplace.json: ${plugins.length} plugins`);

/** A community plugin's listing: its latest release, checked. */
async function resolveCommunity(entry) {
  if (!/^[\w.-]+\/[\w.-]+$/.test(entry.repo ?? "")) throw new Error("repo must be owner/name");
  if (!categories.has(entry.category)) throw new Error(`category must be one of ${[...categories].join(", ")}`);
  const rel = await gh(`/repos/${entry.repo}/releases/latest`);
  if (!rel) throw new Error("it has no release: publish one, as Dalibre installs releases");
  const asset = rel.assets.find((a) => a.name.endsWith(".dalibre-plugin"));
  const pkg = asset ? asset.browser_download_url : `https://api.github.com/repos/${entry.repo}/zipball/${encodeURIComponent(rel.tag_name)}`;
  const res = await fetchMarket(pkg, { headers: auth() });
  if (!res.ok) throw new Error(`its package didn’t download: ${res.status}`);
  const files = unzipSync(new Uint8Array(await res.arrayBuffer()));
  const root = packageRoot(Object.keys(files));
  const m = JSON.parse(strFromU8(files[root + "plugin.json"]));
  validateManifest(m);
  if (process.env.DALIBRE_PLUGIN) {
    const dir = await mkdtemp(join(tmpdir(), "market-"));
    try {
      for (const [name, data] of Object.entries(files)) {
        if (!name.startsWith(root) || name.endsWith("/")) continue;
        const parts = name.slice(root.length).split("/");
        if (parts.some((part) => !part || part === "." || part === ".." || /[\\:\0]/.test(part))) {
          throw new Error("package contains an invalid file path");
        }
        const dst = join(dir, ...parts);
        await mkdir(dirname(dst), { recursive: true });
        await writeFile(dst, data);
      }
      execFileSync(process.env.DALIBRE_PLUGIN, ["check", dir], { stdio: "pipe" });
    } catch (err) {
      throw new Error(`dalibre-plugin check failed: ${err.stderr?.toString().trim() || err.message}`);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  }
  return listing(m, { category: entry.category, calibre: entry.calibre, repo: entry.repo, origin: "community", community: true, package: pkg });
}

function validateManifest(m) {
  for (const key of ["id", "name", "author", "license", "version", "capabilities"]) {
    if (!m[key]) throw new Error(`plugin.json has no ${key}`);
  }
  if (m.api !== 1) throw new Error("plugin.json must say api 1");
  if (reserved(m.id)) throw new Error(`id ${m.id} is kept for built-in plugins`);
  if (!semver.test(m.version)) throw new Error(`version ${m.version} isn’t a semantic version`);
  const caps = m.capabilities;
  if (Array.isArray(caps) ? caps.includes("source") || caps.includes("bookSource") : caps.bookSource != null) {
    throw new Error(`The market only lists library tools; ${m.id} declares a book source`);
  }
}

/** The folder holding plugin.json, as Dalibre finds it in a package. */
function packageRoot(names) {
  if (names.includes("plugin.json")) return "";
  const roots = names.filter((n) => /^[^/]+\/plugin\.json$/.test(n));
  if (roots.length === 1) return roots[0].slice(0, -"plugin.json".length);
  const nested = names.filter((n) => /^[^/]+\/plugin\/plugin\.json$/.test(n));
  if (nested.length === 1) return nested[0].slice(0, -"plugin.json".length);
  throw new Error("no plugin.json at the package’s root or in a plugin folder");
}

function auth() {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function gh(path) {
  const res = await fetchMarket(`https://api.github.com${path}`, { headers: { Accept: "application/vnd.github+json", ...auth() } });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`GitHub answered ${res.status} for ${path}`);
  return res.json();
}

async function fetchMarket(url, options) {
  for (let attempt = 0; ; attempt++) {
    try {
      const res = await fetch(url, { ...options, signal: AbortSignal.timeout(25_000) });
      if (attempt === 2 || (res.status !== 429 && res.status < 500)) return res;
      await res.body?.cancel();
    } catch (err) {
      if (attempt === 2) throw new Error(`${url}: ${err.cause?.message ?? err.message}`);
    }
    await setTimeout(750 * (attempt + 1));
  }
}

/** calibre plugins' total downloads, by lower-cased name, from calibre's stats page. */
async function calibreDownloads() {
  const out = new Map();
  try {
    const response = await fetch("https://plugins.calibre-ebook.com/stats.html", { signal: AbortSignal.timeout(10_000) });
    const html = await response.text();
    for (const m of html.matchAll(/<tr[^>]*>\s*<td[^>]*>([\s\S]*?)<\/td>\s*<td[^>]*>\s*(\d+)\s*<\/td>/g)) {
      out.set(m[1].replace(/<[^>]+>/g, "").replace(/&amp;/g, "&").trim().toLowerCase(), Number(m[2]));
    }
  } catch (err) {
    console.warn(`calibre’s download counts unavailable: ${err.message}`);
  }
  return out;
}
