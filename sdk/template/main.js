// @ts-check
// A metadata plugin. Replace the example URL and response fields with your provider's API.
// The API is described in dalibre.d.ts.

const API = "https://api.example.com";

/**
 * Finds metadata for a book by ISBN or title.
 * @param {Dalibre.Book} book
 * @returns {Promise<Dalibre.Candidate[]>}
 */
export async function identify(book) {
  const isbn = book.identifiers.isbn;
  const title = book.title.trim();
  if (!isbn && !title) return [];

  const query = isbn ? `isbn=${encodeURIComponent(isbn)}` : `q=${encodeURIComponent(title)}`;
  const apiKey = dalibre.settings.apiKey;
  const response = await fetch(`${API}/books?${query}`, {
    headers: apiKey ? { Authorization: `Bearer ${apiKey}` } : {},
  });
  if (response.status === 401) throw dalibre.error("auth", "The API key was refused");
  if (response.status === 429) throw dalibre.error("rateLimited");
  if (!response.ok) throw dalibre.error("unavailable", `example.com answered ${response.status}`);

  const data = await response.json();
  return data.results.slice(0, 20).map((/** @type {any} */ match) => ({
    id: String(match.id),
    title: match.title,
    authors: match.authors,
    language: match.language,
    identifiers: match.isbn ? { isbn: match.isbn } : {},
    cover: match.cover_url,
  }));
}
