import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/invitations"
)({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/organization/$organizationId/settings", params })
  },
  component: () => null,
})
