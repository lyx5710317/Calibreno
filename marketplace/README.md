# Calibreno Marketplace

Calibreno's **Plugins › Marketplace** lists tools for managing a personal
library. It includes existing ports, community releases and tools
maintained by Calibreno. The desktop shows each plugin's author and origin.
Plugins that declare the `bookSource` capability are excluded from the
catalog and disabled in the desktop app. Users can add an OPDS catalog in
Settings › OPDS to browse or import books from that catalog.

## Plugins

| Plugin | Does | Origin |
|---|---|---|
| [Search the Internet](plugins/search-internet) | Opens a book's search on Douban, Goodreads or another chosen site | calibre's Search The Internet |
| [中文书目校对](plugins/zh-metadata-review) | Reviews selected books' Chinese metadata offline and previews suggested corrections | Calibreno |
| [Chinese Text Conversion](plugins/chinese-convert) | Converts EPUBs between Simplified and Traditional Chinese | calibre's Chinese Text Conversion |
| [Extract ISBN](plugins/extract-isbn) | Finds the ISBN printed in a book and sets it | calibre's Extract ISBN |
| [Find Duplicates](plugins/find-duplicates) | Finds books that look like the same book, by title and author or by ISBN | calibre's Find Duplicates |
| [Quality Check](plugins/quality-check) | Finds books with missing or messy metadata | calibre's Quality Check |
| [Clean Metadata](plugins/clean-metadata) | Cleans up titles, authors and series | calibre's Clean Metadata |
| [Manage Series](plugins/manage-series) | Puts books in a series and numbers them | calibre's Manage Series |
| [EPUB Merge](plugins/epub-merge) | Merges EPUBs into one new book | calibre's EpubMerge |
| [EPUB Split](plugins/epub-split) | Splits sections of an EPUB off into new books | calibre's EpubSplit |
| [Modify ePub](plugins/modify-epub) | Fixes and cleans up EPUBs in place | calibre's Modify ePub |
| [KePub](plugins/kepub) | Converts EPUBs to Kobo's KePub | kepubify, calibre's KoboTouchExtended |

Each plugin's README describes its capabilities and credits its sources.

## List your plugin

See the [plugin quick start](../docs/plugins/README.md#quick-start) for a
metadata example. The SDK template includes a workflow that packages your
plugin on each version tag.

Open a pull request adding your plugin's repository to
[community.json](community.json):

```json
{ "repo": "ann/dalibre-example-metadata", "category": "metadata" }
```

- Categories are `library`, `metadata`, `epub` and `language`. The
  catalog accepts library tools and metadata sources.
- `calibre`, optional: the calibre plugin yours does the work of, as
  named on [plugins.calibre-ebook.com](https://plugins.calibre-ebook.com),
  so calibre's users find it.
- Your repository has `plugin.json` at its root (or in a `plugin`
  folder) and at least one release, with a `.dalibre-plugin` package
  (`npx dalibre-plugin pack .`) or without one. If it uses what a newer
  Dalibre added, say so with `"dalibre": ">=0.4.0"`: older ones then
  show it as needing an update rather than offering it. See [Writing
  Dalibre plugins](../docs/plugins/README.md).

The pull request's check downloads your latest release and runs
`dalibre-plugin check` on it. Once merged, the Marketplace lists your
latest release and picks up new ones within a day; users see what your
plugin asks for before they install it.

## Curate the marketplace

- `catalog.json` selects which local plugin folders are published.
  Set the origin to `upstream` for an existing port and `calibreno`
  for a tool developed by Calibreno. Preserve the original author,
  source links and license information for existing ports.
- In `featured`, list the plugin IDs in the order they should appear.
  Remove an ID from that list to stop recommending it. Remove the
  plugin's catalog or community entry to stop offering it. Existing
  installations and published version packages remain available.
- To add a community release, add its repository to `community.json`.
  Its author publishes and maintains the package. The marketplace
  checks the manifest before it lists the release.

## How it works

- `src/<name>/` is each plugin's code; `src/shared/` is code they share.
- `plugins/<name>/` is each plugin as Dalibre installs it: `plugin.json`,
  its README, and `main.js`, which `node build.mjs` bundles from `src/`
  (with npm packages) and git ignores.
- The Marketplace workflow (`.github/workflows/marketplace.yml`) checks
  them with the `dalibre-plugin` of the same commit, so a plugin can use
  what Dalibre's plugin API gains in the same pull request. On `main` it
  packs each plugin version not packed yet into the `marketplace`
  branch's `packages/`, and writes `marketplace.json` there from them and
  community.json's plugins. Calibreno reads the index from the
  [Calibreno marketplace branch](https://github.com/lyx5710317/Calibreno/tree/marketplace).
  The workflow runs again daily for the community's new releases.
  If a configured release fails validation or cannot be fetched, the
  workflow leaves the last published index in place.
  In a fork, `GITHUB_REPOSITORY` makes the generated index refer to that
  fork's first-party packages. Outside GitHub Actions, it defaults to
  `lyx5710317/Calibreno`.

```sh
npm ci
go build -o dalibre-plugin ../cmd/dalibre-plugin
node build.mjs                         # bundle every plugin, or: node build.mjs epub-merge
./dalibre-plugin check plugins/epub-merge
./dalibre-plugin run plugins/epub-merge merge a.epub b.epub -o out
node index.mjs --folders               # list the local folders selected for publication
node index.mjs --check                 # validate the catalog without writing an index
```

To change a plugin, edit `src/<name>/` and bump `version` in its
`plugin.json`: a published version is never packed again, and Dalibre
offers the new one as an update. A plugin using what the next Dalibre
adds raises its `"dalibre"` to that version: `main` publishes it right
away, while Dalibre's release comes later, and the Marketplace offers it
to every Dalibre its `"dalibre"` allows. The workflow checks each plugin
  with the `dalibre-plugin` of the latest stable tag reachable from the
  checkout too, and fails when that version can't load one its `"dalibre"`
  offers it to. It can't see
what `main.js` uses (a new kind of form field, say), so a pull request
that adds to the plugin API and uses it in a plugin raises that
plugin's `"dalibre"` itself.

## License

GPL-3.0-or-later ([LICENSE.md](LICENSE.md)), as the calibre plugins they
are ported from; KePub is MIT, as kepubify.
