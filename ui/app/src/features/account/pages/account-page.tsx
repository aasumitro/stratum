import { Link, useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  IconAdjustmentsHorizontal,
  IconDatabaseExport,
  IconHistory,
  IconKey,
  IconShieldLock,
  IconUser,
} from "@tabler/icons-react"
import { AccountHeader } from "@/features/account/components/account-header"
import { ProfileForm } from "@/features/account/components/profile-form"
import { EmailSection } from "@/features/account/components/email-section"
import { PreferencesSection } from "@/features/account/components/preferences-section"
import { SecuritySection } from "@/features/account/components/security-section"
import { DangerZone } from "@/features/account/components/danger-zone"
import { AuditLogSection } from "@/features/account/components/audit-log-section"
import { ApiTokensSection } from "@/features/account/components/api-tokens-section"
import { LockedTag } from "@/components/shared/permission-guard"
import { cn } from "@/lib/ui"

// Ordered by how often/how urgently each section matters to the account
// owner: identity first, then protecting the account (security), then
// personal taste (preferences), then data rights, then history, then the
// least-used developer surface last.
const TABS: {
  key: string
  labelKey: string
  icon: React.ElementType
  soon?: boolean
}[] = [
  { key: "profile", labelKey: "nav.profile", icon: IconUser },
  { key: "security", labelKey: "account.security", icon: IconShieldLock },
  {
    key: "preferences",
    labelKey: "account.preferences",
    icon: IconAdjustmentsHorizontal,
  },
  {
    key: "privacy",
    labelKey: "account.privacyData.title",
    icon: IconDatabaseExport,
  },
  { key: "auditLog", labelKey: "nav.personalAudit", icon: IconHistory },
  { key: "tokens", labelKey: "nav.apiTokens", icon: IconKey, soon: true },
]

// Account page owns its own left-side sub-nav (page-local, not the global
// sidebar — see SidebarPersonalNav, which only lists Organizations/Account/
// Notifications at the top level). Driven by the ?tab= search param so
// deep links keep working.
export function AccountPage() {
  const { t } = useTranslation()
  const { tab = "profile" } = useSearch({ from: "/_protected/account" })

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <AccountHeader />

      <div className="flex flex-col gap-6 sm:flex-row sm:gap-8">
        <nav className="flex shrink-0 gap-1 overflow-x-auto pb-1 sm:w-48 sm:flex-col sm:overflow-visible sm:pt-1.5 sm:pb-0">
          {TABS.map((item) => (
            <Link
              key={item.key}
              to="/account"
              search={{ tab: item.key } as never}
              className={cn(
                "flex shrink-0 items-center gap-2 rounded-lg px-2.5 py-2 text-sm font-medium whitespace-nowrap transition-colors",
                tab === item.key
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground"
              )}
            >
              <item.icon className="size-4 shrink-0" />
              <span className="sm:flex-1">{t(item.labelKey)}</span>
              {item.soon && (
                <LockedTag
                  label={t("nav.soon")}
                  tooltip={t("nav.apiTokensSoonTooltip")}
                />
              )}
            </Link>
          ))}
        </nav>

        <div className="min-w-0 flex-1">
          {tab === "profile" && (
            <div className="flex flex-col gap-6">
              <EmailSection />
              <ProfileForm />
            </div>
          )}
          {tab === "preferences" && <PreferencesSection />}
          {tab === "security" && <SecuritySection />}
          {tab === "privacy" && <DangerZone />}
          {tab === "auditLog" && <AuditLogSection />}
          {tab === "tokens" && <ApiTokensSection />}
        </div>
      </div>
    </div>
  )
}
