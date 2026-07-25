import { useTranslation } from "react-i18next"
import { Card, CardContent } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import { ExportSection } from "@/features/account/components/export-section"
import { DeleteSection } from "@/features/account/components/delete-section"

// Displays as "Privacy & data" — the component name predates that rename.
export function DangerZone() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold">
          {t("account.privacyData.title")}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {t("account.privacyData.description")}
        </p>
      </div>
      <Card className="border-destructive/30">
        <CardContent className="flex flex-col gap-5">
          <ExportSection />
          <Separator />
          <DeleteSection />
        </CardContent>
      </Card>
    </div>
  )
}
