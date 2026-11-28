import type { APIRoute } from "astro";

const site = "https://dalibre.randynamic.org";

export const GET: APIRoute = () =>
  new Response(
    `# Dalibre

> Dalibre is a lightweight desktop e-book manager that reads and writes calibre libraries. The Calibreno Marketplace offers sandboxed JavaScript plugins for metadata lookup, file types and library actions. Dalibre serves the library to AI agents over MCP and sends books to KOReader through calibre's wireless device connection.

## Docs

- [Dalibre and KOReader](${site}/docs/koreader.md): sending books from Dalibre to KOReader over Wi‑Fi, as KOReader connects to calibre, as one Markdown file.
- [Dalibre MCP](${site}/docs/mcp.md): connecting AI agents to a Dalibre library over MCP, with examples and every tool, as one Markdown file.
- [Writing Dalibre plugins](${site}/docs/plugins.md): the complete plugin guide and API reference (plugin API 1) as one Markdown file.
`,
    { headers: { "Content-Type": "text/plain; charset=utf-8" } },
  );
