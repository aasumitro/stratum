import { IconLock } from "@tabler/icons-react"
import { Card, CardContent } from "@/components/ui/card"
import { EmptyState } from "@/components/shared/empty-state"

export type FeatureGateReason = "plan" | "permission"

interface FeatureGateCardProps {
  reason: FeatureGateReason
  title: string
  description?: string
  cta?: { label: string; onClick: () => void }
}

/**
 * "This feature isn't available to you right now" panel — always a lock
 * icon, copy and CTA fully supplied by the caller. Presentation only: it
 * performs no billing, permission, or feature-flag checks itself, and
 * doesn't decide whether it should render — the parent feature does.
 */
export function FeatureGateCard({
  reason,
  title,
  description,
  cta,
}: FeatureGateCardProps) {
  return (
    <Card data-gate-reason={reason}>
      <CardContent className="p-0">
        <EmptyState
          icon={IconLock}
          title={title}
          description={description}
          action={cta}
        />
      </CardContent>
    </Card>
  )
}
