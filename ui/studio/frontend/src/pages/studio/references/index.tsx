import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import { IconLayersLinked } from "@tabler/icons-react"
import { cn } from "@/lib/ui"
import { CountriesTab } from "./countries-tab"
import { CurrenciesTab } from "./currencies-tab"

type Tab = "countries" | "currencies"

const TABS: { id: Tab; label: string }[] = [
  { id: "countries", label: "Countries" },
  { id: "currencies", label: "Currencies" },
]

export function ReferencesPage() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const [tab, setTab] = useState<Tab>("countries")

  return (
    <div className="flex flex-col min-h-full">
      {/* Header */}
      <header className="flex items-center gap-3 px-6 py-4 border-b">
        <IconLayersLinked className="size-4 text-muted-foreground" />
        <div className="flex flex-col gap-0.5">
          <h1 className="font-semibold text-sm">References</h1>
          <p className="text-xs text-muted-foreground">
            Seed data for the target project database
          </p>
        </div>
      </header>

      {/* Tabs */}
      <div className="flex border-b px-6">
        {TABS.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={cn(
              "px-4 py-2.5 text-sm border-b-2 transition-colors",
              tab === t.id
                ? "border-primary text-foreground font-medium"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        {tab === "countries" && <CountriesTab projectId={projectId} />}
        {tab === "currencies" && <CurrenciesTab projectId={projectId} />}
      </div>
    </div>
  )
}
