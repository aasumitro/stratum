import type { ErrorComponentProps } from "@tanstack/react-router"
import { ConnectionErrorPage } from "@/components/shared/connection-error-page"
import { GenericErrorPage } from "@/components/shared/generic-error-page"
import { isConnectionError } from "@/lib/api/error"

// Only a genuine network/CORS/timeout failure gets the "can't reach the
// server" messaging — anything else (a render bug, a non-network loader
// error) gets a generic "something went wrong" page instead of falsely
// telling the user their connection is down.
export function RouteErrorBoundary({ error }: ErrorComponentProps) {
  return isConnectionError(error) ? (
    <ConnectionErrorPage />
  ) : (
    <GenericErrorPage />
  )
}
