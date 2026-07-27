import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/settings/danger"
)({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/organization/$organizationId/settings",
      params,
      hash: "danger",
    })
  },
  component: () => null,
})
