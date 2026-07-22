import type { ReactNode } from "react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { cn } from "@/lib/ui"

interface SideDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  children: ReactNode
  footer?: ReactNode
  className?: string
}

/**
 * Standard Sheet chrome: header (title + description) + scrollable body +
 * optional footer. Replaces the near-identical header/body scaffolding
 * hand-rolled in add-member-sheet, invite-email-sheet, webhook-form-sheet,
 * and import-members-sheet.
 */
export function SideDrawer({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
}: SideDrawerProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        className={cn("flex flex-col gap-0 sm:max-w-md", className)}
      >
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          {description && <SheetDescription>{description}</SheetDescription>}
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-4">{children}</div>
        {footer && <SheetFooter>{footer}</SheetFooter>}
      </SheetContent>
    </Sheet>
  )
}
