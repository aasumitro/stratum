import { IconCodeVariableMinus } from "@tabler/icons-react"
import { EmptyState } from "@/components/shared/empty-state"

interface ReservedPageProps {
  label: string
}

/**
 * Placeholder for a Platform nav slot not built out yet (see the reserved
 * block in sidebar-organization-nav.tsx) — swap for the real page once that
 * feature is designed.
 */
export function ReservedPage({ label }: ReservedPageProps) {
  return (
    <EmptyState
      icon={IconCodeVariableMinus}
      title={label}
      description="Reserved for a future platform feature."
    />
  )
}
