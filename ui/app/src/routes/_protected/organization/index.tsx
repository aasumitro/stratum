import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute("/_protected/organization/")({
  beforeLoad: () => {
    throw redirect({ to: "/organizations" })
  },
  component: () => null,
})
