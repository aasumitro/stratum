import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/webhooks"
)({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/organization/$organizationId/settings",
      params,
      search: { panel: "webhooks" },
    })
  },
  component: () => null,
})
