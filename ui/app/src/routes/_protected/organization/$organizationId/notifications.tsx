import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/notifications"
)({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/notifications",
      search: { organization_id: params.organizationId },
    })
  },
  component: () => null,
})
