import type { ReactNode } from "react"
import { IconLock } from "@tabler/icons-react"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/ui"

interface LockedTagProps {
  label: string
  tooltip: string
  className?: string
}

/**
 * Lock icon + role tag + tooltip for a nav item that's visible but not
 * accessible to the current role — nav shape never mutates.
 */
export function LockedTag({ label, tooltip, className }: LockedTagProps) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Badge
            variant="outline"
            className={cn(
              "gap-1 border-transparent bg-transparent text-[10px] text-muted-foreground",
              className
            )}
          />
        }
      >
        <IconLock className="size-3" />
        {label}
      </TooltipTrigger>
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  )
}

interface AccessDeniedExplainerProps {
  title: string
  /** must NAME who can help, e.g. "Ask Jane Cooper (owner) to enable this." */
  description: ReactNode
  action?: { label: string; onClick: () => void }
}

/** 403 explainer: what failed + who can help + one recovery action. */
export function AccessDeniedExplainer({
  title,
  description,
  action,
}: AccessDeniedExplainerProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-4 rounded-xl border border-dashed py-16 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-muted">
        <IconLock className="size-7 text-muted-foreground" />
      </div>
      <div>
        <p className="font-semibold">{title}</p>
        <p className="mt-1 max-w-sm text-sm text-muted-foreground">
          {description}
        </p>
      </div>
      {action && (
        <Button size="sm" onClick={action.onClick}>
          {action.label}
        </Button>
      )}
    </div>
  )
}

interface PermissionGuardProps {
  allowed: boolean
  explainer: AccessDeniedExplainerProps
  children: ReactNode
}

/** Renders children when allowed, otherwise the 403 explainer. */
export function PermissionGuard({
  allowed,
  explainer,
  children,
}: PermissionGuardProps) {
  if (!allowed) return <AccessDeniedExplainer {...explainer} />
  return <>{children}</>
}
