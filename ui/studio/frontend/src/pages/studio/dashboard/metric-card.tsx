import { Card } from "@/components/ui/card"

interface MetricCardProps {
  icon: React.ReactNode
  label: string
  value: string
  sub?: string
}

export function MetricCard({ icon, label, value, sub }: MetricCardProps) {
  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-1 min-w-0">
          <span className="text-xs text-muted-foreground font-medium">{label}</span>
          <span className="text-2xl font-semibold tabular-nums">{value}</span>
          {sub && <span className="text-xs text-muted-foreground">{sub}</span>}
        </div>
        <div className="rounded-lg bg-muted p-2 flex-shrink-0 text-muted-foreground">
          {icon}
        </div>
      </div>
    </Card>
  )
}
