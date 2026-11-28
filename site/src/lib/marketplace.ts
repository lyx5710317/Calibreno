// The Marketplace's plugins, read in the browser from the same
// marketplace.json Dalibre reads, so the plugins page lists what the app
// offers as soon as it changes.

export const url = "https://raw.githubusercontent.com/lyx5710317/Calibreno/refs/heads/marketplace/marketplace.json";

export interface Category {
  id: string;
  title: string;
}

interface Action {
  id: string;
  title: string;
  scope: "books" | "library";
}

export interface Plugin {
  id: string;
  name: string;
  version: string;
  description: string;
  author?: string;
  license?: string;
  icon?: string;
  homepage?: string;
  dalibre?: string;
  category: string;
  /** The calibre plugin it does the work of. */
  calibre?: string;
  /** calibre's downloads of that plugin, used after featured order. */
  downloads?: number;
  repo?: string;
  capabilities?: string[] | {
    bookSource?: unknown;
    metadata?: { fields?: string[] };
    fileType?: { title?: string };
    actions?: Action[];
  };
  permissions: { hosts?: string[]; localNetwork?: boolean; library?: "read" | "write" };
}

export async function loadMarketplace(): Promise<{ categories: Category[]; featured: string[]; plugins: Plugin[] }> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`fetching the Marketplace: ${res.status} ${res.statusText}`);
  const data = (await res.json()) as { categories?: Category[]; featured?: string[]; plugins?: Plugin[] };
  return {
    categories: (data.categories ?? []).filter((category) => category.id !== "sources"),
    featured: data.featured ?? [],
    plugins: (data.plugins ?? []).filter(({ category, capabilities }) =>
      category !== "sources" && (Array.isArray(capabilities)
        ? !capabilities.includes("source") && !capabilities.includes("bookSource")
        : capabilities?.bookSource == null)),
  };
}

/** What the plugin may reach, in one short line: "Reads your library · Offline". */
export function reach(p: Plugin): string {
  const out: string[] = [];
  if (p.permissions.library === "write") out.push("Changes your library");
  else if (p.permissions.library === "read") out.push("Reads your library");
  const hosts = p.permissions.hosts ?? [];
  if (hosts.length === 1) out.push(hosts[0]);
  else if (hosts.length > 1) out.push(`${hosts.length} sites`);
  if (p.permissions.localNetwork) out.push("Local network");
  if (!hosts.length && !p.permissions.localNetwork) out.push("Offline");
  return out.join(" · ");
}
