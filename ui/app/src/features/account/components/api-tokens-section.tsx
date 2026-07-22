import { useTranslation } from "react-i18next"
import { IconKey } from "@tabler/icons-react"
import { EmptyState } from "@/components/shared/empty-state"

// Explicit placeholder tab per the App Shell design — not a scope cut.
export function ApiTokensSection() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold">{t("nav.apiTokens")}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("account.apiTokens.description")}
        </p>
      </div>
      <EmptyState
        icon={IconKey}
        title={t("account.apiTokens.comingSoon")}
        description={t("account.apiTokens.comingSoonDescription")}
      />
    </div>
  )
}
