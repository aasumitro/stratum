import { useState } from "react"

type Page<T> = { data?: T[] | null; next_cursor?: string } | undefined

// Accumulates successive cursor-paginated pages into one growing list, for
// "Load More" UX. Takes the resolved data shape rather than a whole query
// result, so it composes with any useHTTPQuery-based cursor-paginated hook.
//
// `cursor` is the *requested* cursor (the caller's own paging state), not
// `page.next_cursor`. It tells this hook whether `page` is a fresh first
// page (replace) or a "Load more" continuation (append) — a mount-only ref
// can't make that call, since `page` gets a new object reference on any
// background refetch (SSE-driven invalidation, a mutation invalidating the
// list, a filter change that resets the caller's cursor to undefined) even
// when it's still page 1, not just on an explicit "Load more" click.
//
// Adjusts state during render (React's documented pattern for "state that
// depends on a changed prop") instead of an effect + setState, so a
// background refetch resolves to the correct list in the same render pass.
export function useCursorAccumulator<T>(
  page: Page<T>,
  cursor: string | undefined
): { items: T[]; nextCursor: string | undefined } {
  // Lazy-initialize from whatever `page` already is at mount — React Query
  // returns cached data synchronously (e.g. navigating back to a route
  // within staleTime), so `page` can already be resolved on the very first
  // render. Seeding `items` as always-empty and relying on the `page !==
  // prevPage` check below to populate it would miss that case: `prevPage`
  // starts equal to `page`, so the check is false on that first render and
  // the already-available data would never make it into `items`.
  const [items, setItems] = useState<T[]>(() => page?.data ?? [])
  const [prevPage, setPrevPage] = useState<Page<T>>(page)

  if (page !== prevPage) {
    setPrevPage(page)
    if (page?.data) {
      const merged =
        cursor === undefined ? page.data! : [...items, ...page.data!]
      setItems(
        Array.from(
          new Map(
            merged.map((item) => [
              item && typeof item === "object" && "id" in item
                ? (item as { id: unknown }).id
                : item,
              item,
            ])
          ).values()
        )
      )
    }
  }

  return { items, nextCursor: page?.next_cursor }
}
