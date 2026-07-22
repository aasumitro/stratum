import { useTranslation } from "react-i18next"
import { IconPalette } from "@tabler/icons-react"
import { EmptyState } from "@/components/shared/empty-state"

// Branding is an intentional placeholder ("Coming soon" card with
// logo/color slots), not an unfinished feature.
export function OrganizationBrandingTab() {
  const { t } = useTranslation()
  return (
    <EmptyState
      icon={IconPalette}
      title={t("organization.settings.brandingComingSoon")}
      description={t("organization.settings.brandingComingSoonDescription")}
    />
  )
}
