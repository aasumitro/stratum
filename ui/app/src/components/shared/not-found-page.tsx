import { Link } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"

export function NotFoundPage() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 text-center">
      <p className="text-6xl font-extrabold text-muted-foreground">404</p>
      <h1 className="text-xl font-bold">Page not found</h1>
      <p className="max-w-xs text-sm text-muted-foreground">
        The page you're looking for doesn't exist or has been moved.
      </p>
      <Button nativeButton={false} render={<Link to="/organizations" />}>
        Go to organizations
      </Button>
    </div>
  )
}
