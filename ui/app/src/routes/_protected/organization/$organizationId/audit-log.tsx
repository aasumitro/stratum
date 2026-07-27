import { createFileRoute, redirect } from "@tanstack/react-router"

// Legacy filtered audit-log links (`/audit-log?actor=X&range=30d`) keep
// working — the old actor/action/resource/range params pass through
// unchanged into Settings' own search schema, which now owns them.
export const Route = createFileRoute(
  "/_protected/organization/$organizationId/audit-log"
)({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/organization/$organizationId/settings",
      params,
      search: { ...(search as Record<string, unknown>), panel: "audit-log" },
    })
  },
  component: () => null,
})
