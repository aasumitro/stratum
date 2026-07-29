import { IconCodeVariableMinus } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"
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
  const { t } = useTranslation()
  return (
    <EmptyState
      icon={IconCodeVariableMinus}
      title={label}
      description={t("common.reservedFeature")}
    />
  )
}
