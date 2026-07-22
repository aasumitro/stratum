import type { AuditEvent } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

export function statusColor(code: number): string {
  if (code >= 500) return "bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20"
  if (code >= 400) return "bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20"
  if (code >= 200) return "bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20"
  return "bg-muted text-muted-foreground"
}

export function methodColor(method: string): string {
  const m: Record<string, string> = {
    GET:    "bg-blue-500/10 text-blue-700 dark:text-blue-400",
    POST:   "bg-green-500/10 text-green-700 dark:text-green-400",
    PATCH:  "bg-amber-500/10 text-amber-700 dark:text-amber-400",
    DELETE: "bg-red-500/10 text-red-700 dark:text-red-400",
    PUT:    "bg-purple-500/10 text-purple-700 dark:text-purple-400",
  }
  return m[method] ?? "bg-muted text-muted-foreground"
}

export function AuditDetailSheet({ event, onClose }: { event: AuditEvent; onClose: () => void }) {
  let prettyMeta = ""
  try {
    // metadata comes from the API as a JSONB string or embedded object
    const raw = (event as unknown as Record<string, unknown>).metadata
    prettyMeta = JSON.stringify(typeof raw === "string" ? JSON.parse(raw) : raw ?? {}, null, 2)
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
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-xl overflow-y-auto overflow-x-hidden">
        <SheetHeader className="pb-4">
          <SheetTitle className="font-mono text-sm break-all leading-snug">
            {event.action} {event.resource}
          </SheetTitle>
          <SheetDescription>{new Date(event.created_at).toLocaleString()}</SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          <div className="rounded-lg border divide-y text-sm">
            {rows.map(([label, value]) => (
              <div key={label} className="flex gap-3 px-4 py-2.5">
                <span className="w-28 shrink-0 text-xs text-muted-foreground">{label}</span>
                <span className="font-mono text-xs break-all">{value}</span>
              </div>
            ))}
          </div>

          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-muted-foreground">Metadata</span>
            <pre className="rounded-lg border bg-muted/40 px-4 py-3 text-xs font-mono whitespace-pre-wrap break-all max-h-64 overflow-y-auto">
              {prettyMeta}
            </pre>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
