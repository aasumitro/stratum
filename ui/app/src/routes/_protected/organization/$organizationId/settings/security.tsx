import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings/security"
)({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/organization/$organizationId/settings",
      params,
      hash: "security",
    })
  },
  component: () => null,
})
