import { useState } from "react"
import type { UserOrganization } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useChangePlan } from "@/hooks/use-support"
import { usePlans } from "@/hooks/use-catalog"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

interface ChangePlanSheetProps {
  projectId: string
  authSub: string
  organization: UserOrganization
  onClose: () => void
}

export function ChangePlanSheet({ projectId, authSub, organization, onClose }: ChangePlanSheetProps) {
  const { data: plans } = usePlans(projectId)
  const changePlan = useChangePlan(projectId, authSub)

  const [plan, setPlan] = useState(organization.plan)
  const [cycle, setCycle] = useState<"monthly" | "yearly">("monthly")

  const handleSubmit = () => {
    changePlan.mutate(
      { organizationID: organization.id, plan, cycle },
      { onSuccess: onClose },
    )
  }

  return (
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-md overflow-y-auto">
        <SheetHeader className="pb-4">
          <SheetTitle>Change plan — {organization.name}</SheetTitle>
          <SheetDescription>
            Direct DB update. No proration or invoice is generated.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cp-plan">Plan</Label>
            <select
              id="cp-plan"
              value={plan}
              onChange={(e) => setPlan(e.target.value)}
              className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              {(plans ?? []).map((p) => (
                <option key={p.id} value={p.id}>{p.name} ({p.id})</option>
              ))}
            </select>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cp-cycle">Cycle</Label>
            <select
              id="cp-cycle"
              value={cycle}
              onChange={(e) => setCycle(e.target.value as "monthly" | "yearly")}
              className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              <option value="monthly">Monthly</option>
              <option value="yearly">Yearly</option>
            </select>
          </div>
        </div>

        <SheetFooter className="pt-4 mt-auto">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={handleSubmit} disabled={changePlan.isPending || !plan}>
            {changePlan.isPending ? "Saving…" : "Change plan"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
