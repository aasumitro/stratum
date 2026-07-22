import { createFileRoute } from "@tanstack/react-router"
import { FilesPage } from "@/features/organization/pages/files-page"

export const Route = createFileRoute(
  "/_protected/organization/$organizationId/files"
)({
  component: FilesPage,
})
