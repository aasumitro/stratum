import { createFileRoute } from "@tanstack/react-router"
import { AccountPage } from "@/features/account/pages/account-page"

const TABS = [
  "profile",
  "preferences",
  "security",
  "privacy",
  "auditLog",
  "tokens",
] as const
type AccountTab = (typeof TABS)[number]

export const Route = createFileRoute("/_protected/account")({
  validateSearch: (search: Record<string, unknown>): { tab?: AccountTab } => ({
    tab: TABS.includes(search.tab as AccountTab)
      ? (search.tab as AccountTab)
      : undefined,
  }),
  component: AccountPage,
})
