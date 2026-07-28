import type { ReactNode } from "react"
import { Skeleton } from "@/components/ui/skeleton"
import { ErrorState } from "@/components/shared/error-state"

interface SettingsSectionProps {
  id: string
  title: string
  titleBadge?: ReactNode
  description?: string
  actions?: ReactNode
  docLinks?: ReactNode
  isLoading?: boolean
  isError?: boolean
  errorMessage?: string
  onRetry?: () => void
  children: ReactNode
}

/**
 * Two-column Settings section: title/description/actions/docs on the left,
 * live content on the right. Stacks to a single column below 768px (matches
 * `DataTable`'s existing card-mode breakpoint). `scroll-mt-16` clears the
 * sticky `SettingsAnchorNav` so a hash jump never hides the section title.
 */
export function SettingsSection({
  id,
  title,
  titleBadge,
  description,
  actions,
  docLinks,
  isLoading,
  isError,
  errorMessage,
  onRetry,
  children,
}: SettingsSectionProps) {
  return (
    <section
      id={id}
      aria-labelledby={`${id}-heading`}
      className="scroll-mt-16 border-t border-border py-16 first-of-type:border-t-0 first-of-type:pt-0"
    >
      <div className="mx-auto grid w-full max-w-7xl grid-cols-1 gap-6 px-2 md:grid-cols-[minmax(0,280px)_1fr] md:gap-12 md:px-4 lg:px-6">
        <div className="flex flex-col gap-4">
          <div>
            <h2
              id={`${id}-heading`}
              className="flex items-center gap-2 font-heading text-base font-medium"
            >
              {title}
              {titleBadge}
            </h2>
            {description && (
              <p className="mt-1 text-sm text-muted-foreground">
                {description}
              </p>
            )}
          </div>
          {actions && (
            <div className="flex flex-col items-start gap-2">{actions}</div>
          )}
          {docLinks}
        </div>
        <div className="min-w-0">
          {isLoading ? (
            <Skeleton className="h-32 w-full" />
          ) : isError ? (
            <ErrorState message={errorMessage} onRetry={onRetry} />
          ) : (
            children
          )}
        </div>
      </div>
    </section>
  )
}
