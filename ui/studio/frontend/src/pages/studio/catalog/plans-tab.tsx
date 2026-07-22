import { useState } from "react"
import {
  IconAdjustmentsHorizontal,
  IconAlertTriangle,
  IconEye,
  IconPencil,
  IconPlus,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react"
import type {
  Feature,
  Plan,
  PlanInput,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useCreatePlan,
  useDeletePlan,
  useDeletePlanFeature,
  useFeatures,
  usePlanFeatures,
  usePlans,
  useUpdatePlan,
  useUpsertPlanFeature,
} from "@/hooks/use-catalog"
import { useCurrencies } from "@/hooks/use-references"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import {
  WriteBanner,
  TableSkeleton,
  formatJSON,
  buildPricesStub,
  formatMetricValue,
  FieldHint,
  PriceUnitHint,
} from "../components/table-helpers"
import { EntitlementsSheet } from "./entitlements-sheet"
import { PlanPreviewDialog } from "./plan-preview-dialog"
import { SaveConfirmDialog } from "./save-confirm-dialog"

const EMPTY_PLAN: PlanInput = {
  id: "",
  name: "",
  description: "",
  prices: "{}",
  sort_order: 0,
  active: true,
}

interface PlanFormProps {
  plan?: Plan
  projectId: string
  onClose: () => void
}

function PlanForm({ plan, projectId, onClose }: PlanFormProps) {
  const isEdit = !!plan
  const create = useCreatePlan(projectId)
  const update = useUpdatePlan(projectId)
  const pending = create.isPending || update.isPending
  const { data: currencies } = useCurrencies(projectId)

  const [form, setForm] = useState<PlanInput>(
    plan
      ? {
          id: plan.id,
          name: plan.name,
          description: plan.description,
          prices: plan.prices,
          sort_order: plan.sort_order,
          active: plan.active,
        }
      : EMPTY_PLAN,
  )

  const set = <K extends keyof PlanInput>(k: K, v: PlanInput[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const [confirmOpen, setConfirmOpen] = useState(false)

  const initialForm: PlanInput = plan
    ? { id: plan.id, name: plan.name, description: plan.description, prices: plan.prices, sort_order: plan.sort_order, active: plan.active }
    : EMPTY_PLAN
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  const isValid = form.id.trim() !== "" && form.name.trim() !== ""
  const submitDisabled = pending || !isValid || (isEdit && !isDirty)

  const handleSubmit = () => {
    setConfirmOpen(false)
    if (isEdit) {
      update.mutate({ planID: plan!.id, input: form }, { onSuccess: onClose })
    } else {
      create.mutate(form, { onSuccess: onClose })
    }
  }

  return (
    <>
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-2xl overflow-y-auto">
        <SheetHeader className="pb-4">
          <SheetTitle>{isEdit ? `Edit plan — ${plan!.id}` : "New plan"}</SheetTitle>
          <SheetDescription>
            Changes are written directly to the production database.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          {!isEdit && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="plan-id">ID (slug) *</Label>
              <Input
                id="plan-id"
                placeholder="enterprise"
                value={form.id}
                onChange={(e) => set("id", e.target.value)}
              />
              <FieldHint>
                Unique identifier used internally and referenced by subscriptions (e.g. <code className="font-mono">solo</code>). Cannot be changed after creation.
              </FieldHint>
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="plan-name">Name *</Label>
            <Input
              id="plan-name"
              placeholder="Enterprise"
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
            />
            <FieldHint>Display name shown to customers on the pricing page.</FieldHint>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="plan-desc">Description</Label>
            <Input
              id="plan-desc"
              placeholder="For large teams."
              value={form.description}
              onChange={(e) => set("description", e.target.value)}
            />
            <FieldHint>One-line marketing copy shown under the plan name.</FieldHint>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="plan-sort">Sort order</Label>
              <Input
                id="plan-sort"
                type="number"
                value={form.sort_order}
                onChange={(e) => set("sort_order", Number(e.target.value))}
              />
              <FieldHint>Lower numbers appear first on the pricing page.</FieldHint>
            </div>
            <div className="flex flex-col gap-1.5 justify-end">
              <Label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.active}
                  onChange={(e) => set("active", e.target.checked)}
                  className="rounded"
                />
                Active
              </Label>
              <FieldHint>Inactive plans are hidden from new signups; existing subscribers are unaffected.</FieldHint>
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label htmlFor="plan-prices">Prices (JSONB)</Label>
              <div className="flex items-center gap-3">
                <button
                  type="button"
                  onClick={() => set("prices", buildPricesStub((currencies ?? []).filter((c) => c.active).map((c) => c.code)))}
                  className="text-xs text-muted-foreground hover:text-foreground transition-colors"
                >
                  Generate stub
                </button>
                <button
                  type="button"
                  onClick={() => set("prices", formatJSON(form.prices))}
                  className="text-xs text-muted-foreground hover:text-foreground transition-colors"
                >
                  Format
                </button>
              </div>
            </div>
            <Textarea
              id="plan-prices"
              rows={4}
              value={form.prices}
              onChange={(e) => set("prices", e.target.value)}
              className="font-mono text-xs"
              placeholder='{"USD": {"monthly": 900, "yearly": 9000}}'
            />
            <FieldHint>
              JSON keyed by currency code. Each currency holds <code className="font-mono">monthly</code>/<code className="font-mono">yearly</code> prices.
              This is the recurring price a subscriber pays every billing cycle for this plan.
            </FieldHint>
            <PriceUnitHint />
          </div>
        </div>

        <SheetFooter className="pt-4 mt-auto">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => setConfirmOpen(true)} disabled={submitDisabled}>
            {pending ? "Saving…" : isEdit ? "Save changes" : "Create plan"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>

    {confirmOpen && (
      <SaveConfirmDialog
        title={isEdit ? `Confirm changes — ${plan!.id}` : "Confirm new plan"}
        fields={[
          { label: "ID", value: <code className="font-mono">{isEdit ? plan!.id : form.id}</code> },
          { label: "Name", value: form.name },
          { label: "Description", value: form.description || "—" },
          { label: "Sort order", value: form.sort_order },
          { label: "Active", value: form.active ? "Yes" : "No" },
        ]}
        pricesJSON={form.prices}
        currencies={currencies}
        pending={pending}
        onConfirm={handleSubmit}
        onCancel={() => setConfirmOpen(false)}
      />
    )}
    </>
  )
}

function PlanEntitlementsCell({
  projectId, planId, features,
}: { projectId: string; planId: string; features: Feature[] | null | undefined }) {
  const { data: entitlements, isLoading } = usePlanFeatures(projectId, planId)
  if (isLoading) return <span className="text-xs text-muted-foreground">…</span>
  if (!entitlements || entitlements.length === 0) {
    return <span className="text-xs text-muted-foreground">None</span>
  }
  return (
    <div className="flex flex-wrap gap-1 max-w-xs">
      {entitlements.map((e) => {
        const f = features?.find((x) => x.id === e.feature_id)
        const label = f?.name ?? e.feature_id
        const value = e.limit_value !== null ? formatMetricValue(e.limit_value, f?.metric_key) : undefined
        return (
          <Badge key={e.feature_id} variant="outline" className="text-xs font-normal">
            {label}{value !== undefined ? `: ${value}` : ""}
          </Badge>
        )
      })}
    </div>
  )
}

export function PlansTab({ projectId }: { projectId: string }) {
  const { data: plans, isLoading, error, refetch, isFetching } = usePlans(projectId)
  const { data: features } = useFeatures(projectId)
  const deletePlan = useDeletePlan(projectId)

  const [formTarget, setFormTarget] = useState<Plan | null | "new">(null)
  const [deleteTarget, setDeleteTarget] = useState<Plan | null>(null)
  const [entitlementsTarget, setEntitlementsTarget] = useState<Plan | null>(null)
  const [previewOpen, setPreviewOpen] = useState(false)

  const { data: planFeatures, isLoading: entitlementsLoading } = usePlanFeatures(
    projectId, entitlementsTarget?.id ?? "",
  )
  const upsertPlanFeature = useUpsertPlanFeature(projectId, entitlementsTarget?.id ?? "")
  const deletePlanFeature = useDeletePlanFeature(projectId, entitlementsTarget?.id ?? "")

  return (
    <div className="flex flex-col gap-4">
      <WriteBanner />

      <div className="flex items-center justify-between">
        <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
          <IconRefresh className={`size-4 ${isFetching ? "animate-spin" : ""}`} />
          Refresh
        </Button>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setFormTarget("new")}>
            <IconPlus className="size-4" />
            Add plan
          </Button>
          <Button variant="outline" size="sm" onClick={() => setPreviewOpen(true)} disabled={!plans || plans.length === 0}>
            <IconEye className="size-4" />
            Preview
          </Button>
        </div>
      </div>

      {isLoading ? (
        <TableSkeleton cols={6} />
      ) : error ? (
        <p className="text-sm text-destructive">{String(error)}</p>
      ) : !plans || plans.length === 0 ? (
        <p className="text-sm text-muted-foreground py-8 text-center">No plans found</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Entitlements</TableHead>
              <TableHead className="w-20 text-center">Sort</TableHead>
              <TableHead className="w-20 text-center">Status</TableHead>
              <TableHead className="w-36 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {plans.map((plan: Plan) => (
              <TableRow key={plan.id}>
                <TableCell>
                  <code className="font-mono text-xs">{plan.id}</code>
                </TableCell>
                <TableCell className="font-medium text-sm">{plan.name}</TableCell>
                <TableCell>
                  <PlanEntitlementsCell projectId={projectId} planId={plan.id} features={features} />
                </TableCell>
                <TableCell className="text-center text-xs tabular-nums">{plan.sort_order}</TableCell>
                <TableCell className="text-center">
                  <Badge variant={plan.active ? "default" : "secondary"} className="text-xs">
                    {plan.active ? "active" : "inactive"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0"
                      onClick={() => setEntitlementsTarget(plan)}
                      title="Manage entitlements"
                    >
                      <IconAdjustmentsHorizontal className="size-3.5" />
                    </Button>
                    <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={() => setFormTarget(plan)}>
                      <IconPencil className="size-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                      onClick={() => setDeleteTarget(plan)}
                    >
                      <IconTrash className="size-3.5" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {formTarget !== null && (
        <PlanForm
          plan={formTarget === "new" ? undefined : formTarget}
          projectId={projectId}
          onClose={() => setFormTarget(null)}
        />
      )}

      {previewOpen && plans && (
        <PlanPreviewDialog
          projectId={projectId}
          plans={plans}
          features={features}
          onClose={() => setPreviewOpen(false)}
        />
      )}

      {entitlementsTarget && (
        <EntitlementsSheet
          title={`Entitlements — ${entitlementsTarget.id}`}
          mode="plan"
          projectId={projectId}
          features={features}
          entitlements={planFeatures}
          isLoading={entitlementsLoading}
          onUpsert={(input) => upsertPlanFeature.mutate(input)}
          onRemove={(featureId) => deletePlanFeature.mutate(featureId)}
          onClose={() => setEntitlementsTarget(null)}
        />
      )}

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Delete plan?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Plan <span className="font-mono font-medium">{deleteTarget?.id}</span> will be
              permanently deleted. This will fail if any subscription references it, or if it still has entitlements — remove those first via Manage Entitlements.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setDeleteTarget(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deletePlan.mutate(deleteTarget.id)
                  setDeleteTarget(null)
                }
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
