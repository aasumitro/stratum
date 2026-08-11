import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import { IconInfoCircle, IconTag } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/ui"
import { PlansTab } from "./plans-tab"
import { FeaturesTab } from "./features-tab"
import { CouponsTab } from "./coupons-tab"
import { AddonsTab } from "./addons-tab"
import { CatalogGuideSheet } from "./guide-sheet"

type Tab = "plans" | "features" | "coupons" | "addons"

const TABS: { id: Tab; label: string }[] = [
  { id: "plans", label: "Plans" },
  { id: "features", label: "Features" },
  { id: "coupons", label: "Coupons" },
  { id: "addons", label: "Addons" },
]

export function CatalogPage() {
  const { projectId } = useParams({ from: "/studio/$projectId" })
  const [tab, setTab] = useState<Tab>("plans")
  const [showGuide, setShowGuide] = useState(false)

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center gap-3 border-b px-6 py-4">
        <IconTag className="size-4 text-muted-foreground" />
        <div className="flex flex-1 flex-col gap-0.5">
          <h1 className="text-sm font-semibold">Catalog</h1>
          <p className="text-xs text-muted-foreground">
            Plans, features, coupons, and addons for the target project database
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setShowGuide(true)}>
          <IconInfoCircle className="size-4" />
          How this works
        </Button>
      </header>

      {showGuide && <CatalogGuideSheet onClose={() => setShowGuide(false)} />}

      <div className="flex border-b px-6">
        {TABS.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={cn(
              "border-b-2 px-4 py-2.5 text-sm transition-colors",
              tab === t.id
                ? "border-primary font-medium text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {tab === "plans" && <PlansTab projectId={projectId} />}
        {tab === "features" && <FeaturesTab projectId={projectId} />}
        {tab === "coupons" && <CouponsTab projectId={projectId} />}
        {tab === "addons" && <AddonsTab projectId={projectId} />}
      </div>
    </div>
  )
}
