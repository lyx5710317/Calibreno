# Changelog

Each version's section is the release notes that Dalibre's update window
shows, so write them for readers of the app. `mygo build` needs a
`## <version>` section for the version in `cmd/dalibre/mygo.json`.

## Unreleased

## 0.8.3

- 已绑定豆瓣书页的书籍，可以从详情栏的书名下方直接打开对应页面。
- 支持「豆瓣书目链接」插件，可手动绑定豆瓣书页，或按 ISBN 查找并确认对应版本。

## 0.8.2

- 支持自研「日文假名标注」插件。可离线为日文 EPUB 添加三种范围的平假名注音，保留书中已有的读音和原始 EPUB。
- 修复插件改写书籍格式后，点击「撤销」时临时文件可能提前清理的问题。

## 0.8.1

- 桌面应用名称更新为 Calibreno，并补齐简体中文界面、插件入口和常用提示。
- 更新应用图标和侧栏标志。
- 新增「待整理」与批次结果。导入后可以复核重复项、批量补资料，并在重新打开应用后继续处理。
- 批量元数据支持 NeoDB 查询、字段预览和失败项单独重试。
- 新增只读「全库查重」，按格式和文件内容找出完全相同的文件。
- Windows x64 新增桌面格式转换，支持 Kindle 格式、EPUB 横竖排和 PDF 逐页 OCR。
- 支持 BOOXDrop 局域网传书和微信读书网页辅助导入，USB Kindle 可以在发送时按需转换 EPUB。
- Kindle、微信读书、BOOX 和 KOReader 的传书配置统一移至「传送设置」，书籍菜单新增「传输」子菜单。
- MCP 设置新增 TRAE、TraeWork、Qoder、QoderWork、Kimi Code、Qwen Code、CodeBuddy 和 Cherry Studio 的可复制配置。
- 移除内置古登堡书源和下载书籍的插件支持。旧书源插件停止运行，已导入的书籍继续可用。
- 在「设置 → OPDS」添加目录后，可从书库侧栏浏览和导入选中的书。添加可搜索的目录后会显示「获取书籍」，连接失败时可重试。
- 插件市场改用 Calibreno 的发布目录，首推「在网上搜书」，并显示插件来源。精选目录可由发布清单维护。

## 0.8.0

- Settings › Library can keep the original characters of titles and
  authors in the names of books' folders and files: turn on Keep
  original characters in file names, and books you add or rename are
  stored as “刘慈欣/三体” rather than “Liu Ci Xin/San Ti”, so your
  system's search finds them by name. calibre still renames a book back
  to ASCII when you edit it there.

## 0.7.0

- Right-click the list's header to show, hide and rearrange its columns:
  Publisher, Tags, Language, Published and Modified are new, and Reset
  columns brings back the defaults. Dalibre keeps your columns, their
  order and widths across launches.
- Scrolling through a large library uses far less memory: covers that
  are no longer on screen are let go and load again when they show.
- Dalibre speaks Brazilian Portuguese.
- Plugin forms can offer a searchable list to choose from, and plugin
  actions can open several pages at once, as the Search the Internet
  plugin's site picker does.
- Plugins sourced from the Marketplace update from it again, where some
  failed with "the package has no plugin.json at its root".
- A plugin no longer asks again for permissions you already gave it to
  open pages or fetch covers.
- Plugin actions can write Word Wise files to a Kindle plugged in as a
  USB drive: the plugin says what it wants to read or write, and Dalibre
  reaches only the `.sdr` folder beside a book on it and its Word Wise
  dictionaries. That folder also holds your highlights, notes and
  reading position, so the permission's warning names them.

## 0.6.3

- On macOS, a library on a network drive mounted in Finder (SMB or AFP)
  opens, where Dalibre used to quit.
- A book sent to a reader over Wireless shows its cover on the reader
  while it arrives.

## 0.6.2

- Dalibre no longer quits before its window shows when calibre's last
  library is on a network drive (SMB or AFP): the welcome screen offers
  it without opening it, so it shows no book count until you open it.
- Export to folder, in a book's menu and for a selection, copies books'
  files to a folder you choose, named from their details with a calibre
  save template, with their covers and OPF if you like.
- Email books to a Kindle's Send to Kindle address: add your Kindles and
  the account to send from in Settings › Email, then choose Email to
  from a book's menu or a selection.

## 0.6.1

- A Kindle plugged in as a USB drive (the ones before 2024) is a library
  of its own, below the recent libraries: browse, search and sort its
  books, send books to it, and remove them, in the library calibre keeps
  on it.
- Your library's books that are on a reader plugged in wear its name in
  the shelf and the list, and a book's details send it to the reader or
  remove it from there.
- A Kindle whose drive isn't mounted shows as Not mounted: choosing it
  mounts it and opens its library.
- A plugin that doesn't load offers the update that may fix it.
- dalibre-plugin downloads a book once, even when the plugin fetches it
  with a program such as pandoc.

## 0.6.0

- Open, in a book's details, opens it in the editor or in your system's
  app for its format.
- Plugins can use programs such as pandoc: Dalibre downloads them for
  you, or uses the ones Homebrew, winget or Scoop installed.
- Install from GitHub shows a plugin's README.
- Plugins go through the proxy set in your system's settings.
- The editor shows a book's images on Windows.

## 0.5.1

- Edit book opens AZW3 books, to read in Glance or to edit, and saves
  them back as AZW3.

## 0.5.0

- Plugin authors can install dalibre-plugin, the tool that makes and
  checks plugins, from npm: `npm install -g dalibre-plugin`.
- Chinese Text Conversion converts all of a book's text, not just its
  title, authors and contents, in books that write it as HTML entities.

## 0.4.0

- Plugins have their own window, from the toolbar's Plugins button, with
  a Marketplace of calibre's most used plugins and the community's.
- Plugin actions run on the books you choose or the whole library, from
  the book menu or the sidebar's Tools.
- Removing books works in libraries made before calibre 8.

## 0.3.0

- File type plugins work on books' files, as calibre's do: when books are
  added, or from a book's menu.
- AI agents can write, try and install plugins over MCP.
- Plugins use standard APIs (fetch, timers, node:fs) and can be written in
  TypeScript; dalibre.fetch and dalibre.log are removed.
- Plugin secrets such as API keys need to be entered again.
- Dalibre checks for updates every hour.

## 0.2.2

- Edit an EPUB, KEPUB or TXT book's text: choose Edit book from a book's
  menu, or Edit on one of its formats.
- Fetch metadata finds books on Google Books.
- Choose Light or Dark in Settings › General, or follow the system.

## 0.2.1

- Send books to KOReader over Wi‑Fi: turn on Wireless devices in the
  Sharing button, then choose Calibre › Connect in KOReader.
- On macOS, closing the window keeps Dalibre running; click its Dock icon
  to bring it back.

## 0.2.0

- Let AI agents such as Claude Code, Claude Desktop and Cursor use your
  library over MCP: turn it on from the MCP button in the toolbar.
- Sharing over OPDS moves to its own button in the toolbar.

## 0.1.1

- Report a bug or request a feature from Feedback in the sidebar.

## 0.1.0

- The first release of Dalibre: browse, search and edit your calibre
  library, and get new books from Project Gutenberg and plugins.
