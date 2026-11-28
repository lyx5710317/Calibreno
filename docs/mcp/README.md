# Using Calibreno with your agents (MCP)

Calibreno serves the open library to AI agents on your computer over the
[Model Context Protocol](https://modelcontextprotocol.io). An agent can
search your books, read their details and files, edit metadata, add
books and formats, import from OPDS catalogs you configure, and try
plugins while Calibreno is open.

How it is built is in [design.md](design.md).

- [Turn it on](#turn-it-on)
- [Connect an agent](#connect-an-agent)
- [国内客户端](#国内客户端)
- [Examples](#examples)
- [Tools](#tools)
- [Read only](#read-only)
- [See what agents did](#see-what-agents-did)

## Turn it on

Click the **MCP** button in the toolbar (the bot) and turn on the switch,
or open Settings › MCP. The server listens on `http://127.0.0.1:8765/mcp`
(the port is in Settings › MCP) and only programs on this computer that
send its token can connect.

## Connect an agent

Open Settings › MCP › Connect an agent, select your client, and click
**Copy**. The preview masks the token. The copied HTTP setup includes
the current port and token.

**Claude Code**: run it in a terminal.

```sh
claude mcp add --transport http dalibre http://127.0.0.1:8765/mcp --header "Authorization: Bearer <token>"
```

**Claude Desktop** starts servers on stdin and stdout only, so it runs
Calibreno's own bridge, `Calibreno mcp`, which needs no token. Add it to
`claude_desktop_config.json`, then restart Claude Desktop:

```json
{
  "mcpServers": {
    "dalibre": {
      "command": "/Applications/Calibreno.app/Contents/MacOS/Calibreno",
      "args": ["mcp"]
    }
  }
}
```

When Calibreno is closed or its HTTP MCP service is turned off, the bridge
serves the last library in read-only mode. If a configured listener accepts
the connection but authentication or the MCP handshake fails, the bridge
reports the error to the client.

**Cursor**: add it to `~/.cursor/mcp.json`.

```json
{
  "mcpServers": {
    "dalibre": {
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

**New token** in Settings › MCP makes a new token at once; agents set up
with the old one need the new setup.

## 国内客户端

在 Calibreno 打开「设置 › MCP」，启用服务，在「连接 Agent」选择客户端并点击复制。HTTP 预设使用 Streamable HTTP。客户端和 Calibreno 需要运行在同一台电脑，TraeWork 等提供云端和本地选项的应用请选择本地。

| 客户端 | 粘贴或执行配置的位置 |
| --- | --- |
| TRAE 中国版 | 「设置 › MCP › 添加 › 手动添加」。[TRAE 文档](https://docs.trae.cn/ide_add-mcp-servers) |
| TraeWork 桌面版 | 「设置 › MCP › 本地 › 创建 › 手动配置」。[TraeWork 文档](https://docs.trae.cn/work_remote-mcp-server) |
| Qoder | 「连接器 › 添加连接器 › JSON」。Qoder CLI 用户可合并到 `~/.qoder/settings.json`。[Qoder 文档](https://docs.qoder.com/qoder/connectors) · [CLI 文档](https://docs.qoder.com/cli/mcp-reference) |
| QoderWork | 「设置 › 连接器 › 添加 › 从 JSON 导入」。[QoderWork 文档](https://docs.qoder.com/qoderwork/connectors) |
| Kimi Code CLI | 合并到 `~/.kimi-code/mcp.json`，打开新会话后运行 `/mcp`。旧版 Kimi CLI 使用 `~/.kimi/mcp.json`。[Kimi Code 文档](https://www.kimi.com/code/docs/kimi-code-cli/customization/mcp.html) · [旧版 Kimi CLI 文档](https://github.com/MoonshotAI/kimi-cli/blob/main/docs/en/customization/mcp.md) |
| Qwen Code | 合并到 `~/.qwen/settings.json`，重新打开 Qwen Code 后运行 `/mcp`。[Qwen Code 文档](https://qwenlm.github.io/qwen-code-docs/en/users/features/mcp/) |
| CodeBuddy IDE | 「设置 › MCP › 添加 MCP」，将配置合并到打开的 JSON 文件。[CodeBuddy IDE 文档](https://www.codebuddy.cn/docs/ide/User-guide/MCP) · [HTTP 配置示例](https://cloud.tencent.com/document/product/1267/138986) |
| CodeBuddy CLI | 在终端运行复制的命令，配置会加入当前用户的 MCP 服务。[CodeBuddy CLI 文档](https://www.codebuddy.cn/docs/cli/mcp) |
| Cherry Studio | 「设置 › MCP › MCP 服务器 › 添加 › 从 JSON 导入」，然后给需要的助手启用该服务。[Cherry Studio 文档](https://cherryai.com.cn/docs/zh-cn/advanced-basic/extensions/mcp/) |

如果配置文件已有 `mcpServers`，只把复制配置中的 `calibreno` 项合并进去。连接后可让智能体「在 Calibreno 里查找《三体》，只返回书名和作者」，再到 Calibreno 的 MCP 活动中查看调用。仅需查书时，可以关闭「允许 Agent 编辑、添加和删除书籍」。更换端口或令牌后，重新复制并更新客户端配置。

## Examples

Ask in your own words; the agent picks the tools. What it calls is
shown under each.

### Find and read

> What do I have by Ursula K. Le Guin that I haven't rated?

`search_books {"author": "Ursula K. Le Guin"}`, then the books with no
`rating`.

> Which series am I missing books from?

`list_facets {"kind": "series"}`, then `search_books {"series": "…", "sort": "series"}`
for each, comparing `series_index`.

> Summarize the description of The Left Hand of Darkness.

`search_books {"query": "left hand darkness"}`, then `get_book {"id": 12}`,
whose `comments` is the description as plain text.

### Tidy up metadata

> Tag every Liu Cixin book "Chinese SF" and "Hard SF".

`search_books {"author": "Liu Cixin"}`, then one call for all of them:

```json
update_books {"ids": [3, 4, 9], "patch": {"add_tags": ["Chinese SF", "Hard SF"]}}
```

> The tags "sci-fi", "SciFi" and "Science Fiction" are the same; keep
> "Science Fiction".

`list_facets {"kind": "tags"}`, `search_books {"tag": "sci-fi"}` and
`search_books {"tag": "SciFi"}`, then `update_books` with
`"add_tags": ["Science Fiction"], "remove_tags": ["sci-fi", "SciFi"]`.

> Fill in the missing ISBNs and publishers of my recently added books.

`search_books {"recent": true}`, `find_metadata {"id": 21}` for each,
then `update_books` with the values the agent trusts:
`{"publisher": "Tor Books", "identifiers": {"isbn": "9780765382030"}}`.

> Use this picture as the cover of Piranesi: ~/Downloads/piranesi.jpg

`set_cover {"id": 7, "path": "/Users/me/Downloads/piranesi.jpg"}`, or
`"url"` for an image on the web.

### Convert formats

Calibreno doesn't convert; your agent does, with the tools it has, then
adds the result to the book.

> Make an AZW3 of Dune for my Kindle with ebook-convert.

1. `get_book {"id": 5}`: the EPUB's path in `files`.
2. In a terminal:
   `ebook-convert "<the EPUB's path>" /tmp/Dune.azw3`
3. `add_format {"id": 5, "path": "/tmp/Dune.azw3"}`

The file is copied into the book's folder and named as calibre names it;
the book shows AZW3 at once. The converted file has to be outside the
library folder.

> Replace the PDF of Thinking, Fast and Slow with an EPUB made by pandoc,
> and remove the PDF.

`get_book`, the conversion, `add_format` with the `.epub`, then
`remove_format {"id": 14, "format": "PDF"}`.

### Add books

> Add every e-book in ~/Downloads/books.

`add_books {"paths": ["/Users/me/Downloads/books"]}`. Books already in
the library are reported, not added again.

> Find Pride and Prejudice in my OPDS catalog and add the EPUB.

First add a searchable catalog in **Settings › OPDS** and make it
available in **Get books**. Call `find_books {"query": "Pride and Prejudice"}`,
then pass the result's `source` and `id` to `download_book` with
`"format": "EPUB"` if the result offers that format. MCP must be allowed
to change the library for these tools to be available.

### Remove

> Remove the duplicates of Dune, keeping the one with the most formats.

`search_books {"query": "dune"}`, `get_book` for each, then
`remove_books {"ids": [31]}`. Removed books go to the Trash (the Recycle
Bin on Windows), so you can get them back.

### Write a plugin

> Write a Calibreno plugin that supplies metadata for a local test book,
> and try it.

The agent reads the [plugin docs](../plugins/README.md), then
`create_plugin {"path": "/Users/me/example-metadata", "id": "io.github.me.examplemetadata"}`.
It applies the example metadata manifest in the plugin guide to
`plugin.json`, exports `identify` in `main.js`, and calls
`add_plugin {"path": "/Users/me/example-metadata"}`. Calibreno prompts
you to approve the requested permissions. The agent can then call
`test_plugin {"id": "io.github.me.examplemetadata", "call": "identify", "book": {"title": "Dune"}}`,
read the result and logs, and use `reload_plugins` after an edit. The
loaded plugin appears in **Edit metadata › Fetch metadata**.

## Tools

| Tool | | Read only |
|---|---|---|
| `search_books` | Searches and filters the books: query, author, series, tag, publisher, language, format, recently added; sorted; 50 at a time. | ✓ |
| `get_book` | A book's metadata, description, identifiers, and the paths of its files and cover. | ✓ |
| `list_facets` | Authors, series, tags, publishers, languages or formats, with how many books have each. | ✓ |
| `update_books` | Changes the fields given of one or more books; `add_tags` and `remove_tags` for tagging many. | |
| `set_cover` | Replaces a book's cover with an image file or URL. | |
| `add_books` | Adds e-book files, or every e-book in folders. | |
| `remove_books` | Moves books to the Trash. | |
| `add_format` | Adds a file to a book as a format, or replaces the one it has. | |
| `remove_format` | Deletes one format of a book. | |
| `find_books` | Searches the OPDS catalogs you configured and made available in Get books. | |
| `download_book` | Adds a result from one of those catalogs to the library. | |
| `find_metadata` | Looks a book up with the metadata plugins. | |
| `list_plugins` | The plugins: loaded, off, waiting for you to allow them, or failed and why. | |
| `create_plugin` | Starts a plugin in an empty folder from the template. | |
| `add_plugin` | Adds a plugin from its folder; you are asked to allow what it reaches. | |
| `reload_plugins` | Loads the plugins again after an edit. | |
| `test_plugin` | Calls a plugin's `identify` or `process` and returns the result, logs and error. | |
| `enable_plugin` | Turns a plugin on or off. | |
| `remove_plugin` | Removes a plugin added from a folder; the folder stays. | |
| `list_actions` | Lists what the plugins can do with books and the library: their actions. | |
| `run_action` | Runs an action and returns what it found and wants changed. Forms are answered with `answers`; nothing is written without `apply`. | |

Settings › MCP lists them too.

## Read only

Agents get every tool by default. Turn off **Let agents edit, add and
remove books** in Settings › MCP and they get the read tools only; the
MCP popover then says "Running · read only", with **Allow changes**.

## See what agents did

While an agent works, the MCP button shows it: its icon (or initials)
and name. Its popover lists the agents and their latest calls in words,
as "Searched “Dune” · 1 book"; click a call for its arguments. The list
holds the last 500 calls while Calibreno runs.
