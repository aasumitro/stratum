import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/"
)({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/organization/$organizationId/members", params })
  },
  component: () => null,
})
