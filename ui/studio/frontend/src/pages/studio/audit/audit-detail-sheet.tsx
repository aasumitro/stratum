import type { AuditEvent } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

export function AuditDetailSheet({
  event,
  onClose,
}: {
  event: AuditEvent
  onClose: () => void
}) {
  let prettyMeta: string
  try {
    // metadata comes from the API as a JSONB string or embedded object
    const raw = (event as unknown as Record<string, unknown>).metadata
    prettyMeta = JSON.stringify(
      typeof raw === "string" ? JSON.parse(raw) : (raw ?? {}),
      null,
      2
    )
  } catch {
    prettyMeta = "{}"
  }

  const rows: Array<[string, string]> = [
    ["ID", event.id],
    ["Actor", event.actor],
    ["Method", event.action],
    ["Resource", event.resource],
    ["Status code", String(event.status_code)],
    ["Organization", event.organization_id || "—"],
    ["IP", event.ip || "—"],
    ["Time", new Date(event.created_at).toLocaleString()],
  ]

  return (
    <Sheet
      open
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <SheetContent className="flex w-full flex-col gap-0 overflow-x-hidden overflow-y-auto sm:max-w-xl">
        <SheetHeader className="pb-4">
          <SheetTitle className="font-mono text-sm leading-snug break-all">
            {event.action} {event.resource}
          </SheetTitle>
          <SheetDescription>
            {new Date(event.created_at).toLocaleString()}
          </SheetDescription>
        </SheetHeader>

        <div className="mx-6 flex flex-col gap-4 py-4">
          <div className="divide-y rounded-lg border text-sm">
            {rows.map(([label, value]) => (
              <div key={label} className="flex gap-3 px-4 py-2.5">
                <span className="w-28 shrink-0 text-xs text-muted-foreground">
                  {label}
                </span>
                <span className="font-mono text-xs break-all">{value}</span>
              </div>
            ))}
          </div>

          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-muted-foreground">Metadata</span>
            <pre className="max-h-64 overflow-y-auto rounded-lg border bg-muted/40 px-4 py-3 font-mono text-xs break-all whitespace-pre-wrap">
              {prettyMeta}
            </pre>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
