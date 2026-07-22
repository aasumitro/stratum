import type { ReactNode } from "react"
import type { TablerIcon } from "@tabler/icons-react"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Skeleton } from "@/components/ui/skeleton"
import { EmptyState } from "@/components/shared/empty-state"
import { ErrorState } from "@/components/shared/error-state"
import { cn } from "@/lib/ui"

export interface DataTableColumn<T> {
  key: string
  header: ReactNode
  cell: (row: T) => ReactNode
  className?: string
  headerClassName?: string
  /** ≤768px "read-only" mode — omit this column's value from
   * the stacked mobile card view entirely (e.g. an actions column with
   * mutating buttons) rather than rendering it inert. */
  hideOnMobile?: boolean
}

interface DataTableEmptyProps {
  icon: TablerIcon
  title: string
  description?: string
  action?: { label: string; onClick: () => void }
}

interface DataTableProps<T> {
  columns: DataTableColumn<T>[]
  rows: T[]
  rowKey: (row: T) => string
  isLoading?: boolean
  isError?: boolean
  onRetry?: () => void
  /** number of skeleton rows shown while loading — should mirror the real row count */
  skeletonRows?: number
  empty: DataTableEmptyProps
  onRowClick?: (row: T) => void
  className?: string
}

/**
 * The one table shell for the app: skeleton rows that mirror the final
 * layout, built-in empty/error slots, optional row click. Pagination is
 * deliberately not baked in here — compose `DataTablePagination`
 * (page or cursor mode) below it, since pagination state shape varies by
 * page and coupling it into the table would only add an unused prop surface
 * to callers that page differently.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  isLoading,
  isError,
  onRetry,
  skeletonRows = 5,
  empty,
  onRowClick,
  className,
}: DataTableProps<T>) {
  // ≤768px read-only mode — below the `md` breakpoint this
  // renders as stacked label/value cards instead of a horizontally-cramped
  // table, dropping any column marked `hideOnMobile` (mutating actions)
  // entirely rather than rendering an inert control. One shared primitive
  // fix cascades to every page built on DataTable, no per-page work needed.
  const mobileColumns = columns.filter((col) => !col.hideOnMobile)

  return (
    <>
      <div
        className={cn(
          "hidden overflow-hidden rounded-xl border md:block",
          className
        )}
      >
        <Table>
          <TableHeader>
            <TableRow>
              {columns.map((col) => (
                <TableHead key={col.key} className={col.headerClassName}>
                  {col.header}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              Array.from({ length: skeletonRows }).map((_, i) => (
                <TableRow key={i}>
                  {columns.map((col) => (
                    <TableCell key={col.key}>
                      <Skeleton className="h-4 w-full max-w-40" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : isError ? (
              <TableRow>
                <TableCell colSpan={columns.length}>
                  <ErrorState onRetry={onRetry} />
                </TableCell>
              </TableRow>
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={columns.length}>
                  <EmptyState {...empty} />
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row) => (
                <TableRow
                  key={rowKey(row)}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  className={onRowClick ? "cursor-pointer" : undefined}
                >
                  {columns.map((col) => (
                    <TableCell key={col.key} className={col.className}>
                      {col.cell(row)}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <div className={cn("flex flex-col gap-2 md:hidden", className)}>
        {isLoading ? (
          Array.from({ length: skeletonRows }).map((_, i) => (
            <div key={i} className="flex flex-col gap-2 rounded-xl border p-3">
              <Skeleton className="h-4 w-2/3" />
              <Skeleton className="h-4 w-1/2" />
            </div>
          ))
        ) : isError ? (
          <div className="rounded-xl border p-3">
            <ErrorState onRetry={onRetry} />
          </div>
        ) : rows.length === 0 ? (
          <div className="rounded-xl border p-3">
            <EmptyState {...empty} />
          </div>
        ) : (
          rows.map((row) => (
            <div
              key={rowKey(row)}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              className={cn(
                "flex flex-col gap-1.5 rounded-xl border p-3",
                onRowClick && "cursor-pointer"
              )}
            >
              {mobileColumns.map((col) => (
                <div
                  key={col.key}
                  className="flex items-center justify-between gap-3 text-sm"
                >
                  <span className="shrink-0 text-xs font-medium text-muted-foreground">
                    {col.header}
                  </span>
                  <span className="min-w-0 truncate text-right">
                    {col.cell(row)}
                  </span>
                </div>
              ))}
            </div>
          ))
        )}
      </div>
    </>
  )
}
