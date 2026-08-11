import type { OperatorLogEntry } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { actionLabel } from "./utils"

export function EntryDetailSheet({
  entry,
  onClose,
}: {
  entry: OperatorLogEntry
  onClose: () => void
}) {
  const rows: Array<[string, string]> = [
    ["Action", actionLabel(entry.action)],
    ["Target ID", entry.target_id],
    ["Detail", entry.detail || "—"],
    ["Project", entry.project_name || entry.project_id],
    ["Time", new Date(entry.performed_at).toLocaleString()],
  ]

  return (
    <Sheet
      open
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <SheetContent className="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-lg">
        <SheetHeader className="pb-4">
          <SheetTitle className="capitalize">
            {actionLabel(entry.action)}
          </SheetTitle>
          <SheetDescription>
            {new Date(entry.performed_at).toLocaleString()}
          </SheetDescription>
        </SheetHeader>
        <div className="mx-6 py-4">
          <div className="divide-y rounded-lg border text-sm">
            {rows.map(([label, value]) => (
              <div key={label} className="flex gap-3 px-4 py-2.5">
                <span className="w-24 shrink-0 text-xs text-muted-foreground">
                  {label}
                </span>
                <span className="font-mono text-xs break-all">{value}</span>
              </div>
            ))}
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
