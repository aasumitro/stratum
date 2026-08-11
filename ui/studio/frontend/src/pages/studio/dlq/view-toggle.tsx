import { cn } from "@/lib/ui"

export type ViewMode = "all" | "dlq"

export function ViewToggle({
  value,
  onChange,
}: {
  value: ViewMode
  onChange: (v: ViewMode) => void
}) {
  return (
    <div className="inline-flex gap-1 rounded-lg border bg-muted p-1">
      {(["all", "dlq"] as ViewMode[]).map((mode) => (
        <button
          key={mode}
          onClick={() => onChange(mode)}
          className={cn(
            "rounded-md px-3 py-1 text-xs font-medium transition-colors",
            value === mode
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground"
          )}
        >
          {mode === "all" ? "All Queues" : "Dead Letter Only"}
        </button>
      ))}
    </div>
  )
}
