import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  Pagination as PaginationRoot,
  PaginationContent,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination"

interface PagePaginationProps {
  mode?: "page"
  page: number
  totalPages: number
  onPageChange: (page: number) => void
}

interface CursorPaginationProps {
  mode: "cursor"
  hasMore: boolean
  isLoadingMore?: boolean
  onLoadMore: () => void
  loadMoreLabel?: string
}

/**
 * Pluggable pagination footer for DataTable: page-number mode (offset APIs)
 * or "Load more" cursor mode (paired with `useCursorAccumulator`).
 */
export function DataTablePagination(
  props: PagePaginationProps | CursorPaginationProps
) {
  if (props.mode === "cursor") {
    if (!props.hasMore) return null
    return (
      <div className="flex justify-center">
        <Button
          variant="outline"
          size="sm"
          onClick={props.onLoadMore}
          disabled={props.isLoadingMore}
        >
          {props.isLoadingMore && (
            <IconLoader2 data-icon="inline-start" className="animate-spin" />
          )}
          {props.loadMoreLabel ?? "Load more"}
        </Button>
      </div>
    )
  }

  const { page, totalPages, onPageChange } = props
  if (totalPages <= 1) return null

  return (
    <PaginationRoot>
      <PaginationContent>
        <PaginationItem>
          <PaginationPrevious
            href="#"
            onClick={(e) => {
              e.preventDefault()
              if (page > 1) onPageChange(page - 1)
            }}
            aria-disabled={page <= 1}
            className={page <= 1 ? "pointer-events-none opacity-50" : ""}
          />
        </PaginationItem>
        <PaginationItem>
          <PaginationLink href="#" isActive onClick={(e) => e.preventDefault()}>
            {page}
          </PaginationLink>
        </PaginationItem>
        <PaginationItem className="px-1 text-sm text-muted-foreground">
          / {totalPages}
        </PaginationItem>
        <PaginationItem>
          <PaginationNext
            href="#"
            onClick={(e) => {
              e.preventDefault()
              if (page < totalPages) onPageChange(page + 1)
            }}
            aria-disabled={page >= totalPages}
            className={
              page >= totalPages ? "pointer-events-none opacity-50" : ""
            }
          />
        </PaginationItem>
      </PaginationContent>
    </PaginationRoot>
  )
}
