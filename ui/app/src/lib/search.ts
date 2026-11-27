// The API refuses a search shorter than this (SEARCH_TOO_SHORT): a trigram
// index cannot serve it.
export const MIN_SEARCH_LENGTH = 3

// The term to send for what the user typed: trimmed, or "" (no search) while it
// is still too short.
export function searchTerm(text: string): string {
  const term = text.trim()
  return [...term].length >= MIN_SEARCH_LENGTH ? term : ""
}
