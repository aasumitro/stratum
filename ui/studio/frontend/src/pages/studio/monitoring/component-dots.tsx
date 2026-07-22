import type { ComponentStatus } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"

function compDot(value: string) {
  if (value === "healthy") return "bg-green-500"
  if (value === "unhealthy") return "bg-red-500"
  return "bg-muted-foreground/30"
}

export function ComponentDots({ components }: { components: ComponentStatus }) {
  const items = [
    { key: "postgres", label: "PG",    value: components.postgres  },
    { key: "redis",    label: "Redis", value: components.redis     },
    { key: "rabbitmq", label: "MQ",    value: components.rabbitmq  },
  ]
  return (
    <div className="flex items-center gap-2">
      {items.map(({ key, label, value }) => (
        <span
          key={key}
          className="flex items-center gap-1 text-xs text-muted-foreground"
          title={`${label}: ${value || "unknown"}`}
        >
          <span className={`size-1.5 rounded-full ${compDot(value)}`} />
          {label}
        </span>
      ))}
    </div>
  )
}
