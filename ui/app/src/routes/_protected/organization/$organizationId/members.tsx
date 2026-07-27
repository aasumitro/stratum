import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/members"
)({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/organization/$organizationId/settings",
      params,
      hash: "members",
    })
  },
  component: () => null,
})
