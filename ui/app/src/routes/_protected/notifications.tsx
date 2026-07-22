import { createFileRoute } from "@tanstack/react-router"
import { NotificationPage } from "@/features/notification/pages/notification-page"

export const Route = createFileRoute("/_protected/notifications")({
  validateSearch: (search: Record<string, unknown>) => ({
    organization_id:
      typeof search.organization_id === "string"
        ? search.organization_id
        : undefined,
  }),
  component: NotificationPage,
})
