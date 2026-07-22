import { useState } from "react"
import {
  IconAdjustmentsHorizontal,
  IconAlertTriangle,
  IconPencil,
  IconPlus,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react"
import type {
  Addon,
  AddonInput,
  Feature,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useAddonFeatures,
  useAddons,
  useCreateAddon,
  useDeleteAddon,
  useDeleteAddonFeature,
  useFeatures,
  useUpdateAddon,
  useUpsertAddonFeature,
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
import { SaveConfirmDialog } from "./save-confirm-dialog"

const EMPTY_ADDON: AddonInput = {
  id: "",
  name: "",
  description: "",
  prices: "{}",
  active: true,
}

interface AddonFormProps {
  addon?: Addon
  projectId: string
  onClose: () => void
}

function AddonForm({ addon, projectId, onClose }: AddonFormProps) {
  const isEdit = !!addon
  const create = useCreateAddon(projectId)
  const update = useUpdateAddon(projectId)
  const pending = create.isPending || update.isPending
  const { data: currencies } = useCurrencies(projectId)

  const [form, setForm] = useState<AddonInput>(
    addon
      ? {
          id: addon.id,
          name: addon.name,
          description: addon.description,
          prices: addon.prices,
          active: addon.active,
        }
      : EMPTY_ADDON,
  )

  const set = <K extends keyof AddonInput>(k: K, v: AddonInput[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const [confirmOpen, setConfirmOpen] = useState(false)

  const initialForm: AddonInput = addon
    ? { id: addon.id, name: addon.name, description: addon.description, prices: addon.prices, active: addon.active }
    : EMPTY_ADDON
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  const isValid = form.id.trim() !== "" && form.name.trim() !== ""
  const submitDisabled = pending || !isValid || (isEdit && !isDirty)

  const handleSubmit = () => {
    setConfirmOpen(false)
    if (isEdit) {
      update.mutate({ addonID: addon!.id, input: form }, { onSuccess: onClose })
    } else {
      create.mutate(form, { onSuccess: onClose })
    }
  }

  return (
    <>
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-lg overflow-y-auto">
        <SheetHeader className="pb-4">
          <SheetTitle>{isEdit ? `Edit addon — ${addon!.id}` : "New addon"}</SheetTitle>
          <SheetDescription>
            Changes are written directly to the production database.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          {!isEdit && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="addon-id">ID (slug) *</Label>
              <Input
                id="addon-id"
                placeholder="extra-seats"
                value={form.id}
                onChange={(e) => set("id", e.target.value)}
              />
              <FieldHint>
                Unique identifier used when attaching this addon to a subscription. Cannot be changed after creation.
              </FieldHint>
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="addon-name">Name *</Label>
            <Input
              id="addon-name"
              placeholder="Extra Seats"
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
            />
            <FieldHint>Display name shown to customers when purchasing this addon.</FieldHint>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="addon-desc">Description</Label>
            <Input
              id="addon-desc"
              placeholder="10 additional member seats."
              value={form.description}
              onChange={(e) => set("description", e.target.value)}
            />
            <FieldHint>One-line description of what this addon includes.</FieldHint>
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label htmlFor="addon-prices">Prices (JSONB)</Label>
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
              id="addon-prices"
              rows={4}
              value={form.prices}
              onChange={(e) => set("prices", e.target.value)}
              className="font-mono text-xs"
              placeholder='{"USD": {"monthly": 500, "yearly": 5000}}'
            />
            <FieldHint>
              Same shape as plan prices — JSON keyed by currency, each with <code className="font-mono">monthly</code>/<code className="font-mono">yearly</code> amounts.
              This addon bills <strong>alongside the subscription on the same recurring cycle</strong>, not as a one-time charge.
            </FieldHint>
            <PriceUnitHint />
          </div>

          <div className="flex flex-col gap-1.5">
          <Label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={form.active}
              onChange={(e) => set("active", e.target.checked)}
              className="rounded"
            />
            Active
          </Label>
          <FieldHint>Inactive addons can't be newly attached, but existing attachments keep working.</FieldHint>
          </div>
        </div>

        <SheetFooter className="pt-4 mt-auto">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => setConfirmOpen(true)} disabled={submitDisabled}>
            {pending ? "Saving…" : isEdit ? "Save changes" : "Create addon"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>

    {confirmOpen && (
      <SaveConfirmDialog
        title={isEdit ? `Confirm changes — ${addon!.id}` : "Confirm new addon"}
        fields={[
          { label: "ID", value: <code className="font-mono">{isEdit ? addon!.id : form.id}</code> },
          { label: "Name", value: form.name },
          { label: "Description", value: form.description || "—" },
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

function AddonEntitlementsCell({
  projectId, addonId, features,
}: { projectId: string; addonId: string; features: Feature[] | null | undefined }) {
  const { data: entitlements, isLoading } = useAddonFeatures(projectId, addonId)
  if (isLoading) return <span className="text-xs text-muted-foreground">…</span>
  if (!entitlements || entitlements.length === 0) {
    return <span className="text-xs text-muted-foreground">None</span>
  }
  return (
    <div className="flex flex-wrap gap-1 max-w-xs">
      {entitlements.map((e) => {
        const f = features?.find((x) => x.id === e.feature_id)
        const label = f?.name ?? e.feature_id
        const value = e.limit_value !== null ? `+${formatMetricValue(e.limit_value, f?.metric_key)}` : undefined
        return (
          <Badge key={e.feature_id} variant="outline" className="text-xs font-normal">
            {label}{value !== undefined ? `: ${value}` : ""}
          </Badge>
        )
      })}
    </div>
  )
}

export function AddonsTab({ projectId }: { projectId: string }) {
  const { data: addons, isLoading, error, refetch, isFetching } = useAddons(projectId)
  const { data: features } = useFeatures(projectId)
  const deleteAddon = useDeleteAddon(projectId)

  const [formTarget, setFormTarget] = useState<Addon | null | "new">(null)
  const [deleteTarget, setDeleteTarget] = useState<Addon | null>(null)
  const [entitlementsTarget, setEntitlementsTarget] = useState<Addon | null>(null)

  const { data: addonFeatures, isLoading: entitlementsLoading } = useAddonFeatures(
    projectId, entitlementsTarget?.id ?? "",
  )
  const upsertAddonFeature = useUpsertAddonFeature(projectId, entitlementsTarget?.id ?? "")
  const deleteAddonFeature = useDeleteAddonFeature(projectId, entitlementsTarget?.id ?? "")

  return (
    <div className="flex flex-col gap-4">
      <WriteBanner />

      <div className="flex items-center justify-between">
        <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
          <IconRefresh className={`size-4 ${isFetching ? "animate-spin" : ""}`} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setFormTarget("new")}>
          <IconPlus className="size-4" />
          Add addon
        </Button>
      </div>

      {isLoading ? (
        <TableSkeleton cols={5} />
      ) : error ? (
        <p className="text-sm text-destructive">{String(error)}</p>
      ) : !addons || addons.length === 0 ? (
        <p className="text-sm text-muted-foreground py-8 text-center">No addons found</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Entitlements</TableHead>
              <TableHead className="w-20 text-center">Status</TableHead>
              <TableHead className="w-36 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {addons.map((a: Addon) => (
              <TableRow key={a.id}>
                <TableCell><code className="font-mono text-xs">{a.id}</code></TableCell>
                <TableCell className="font-medium text-sm">{a.name}</TableCell>
                <TableCell>
                  <AddonEntitlementsCell projectId={projectId} addonId={a.id} features={features} />
                </TableCell>
                <TableCell className="text-center">
                  <Badge variant={a.active ? "default" : "secondary"} className="text-xs">
                    {a.active ? "active" : "inactive"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0"
                      onClick={() => setEntitlementsTarget(a)}
                      title="Manage entitlements"
                    >
                      <IconAdjustmentsHorizontal className="size-3.5" />
                    </Button>
                    <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={() => setFormTarget(a)}>
                      <IconPencil className="size-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                      onClick={() => setDeleteTarget(a)}
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
        <AddonForm
          addon={formTarget === "new" ? undefined : formTarget}
          projectId={projectId}
          onClose={() => setFormTarget(null)}
        />
      )}

      {entitlementsTarget && (
        <EntitlementsSheet
          title={`Entitlements — ${entitlementsTarget.id}`}
          mode="addon"
          projectId={projectId}
          features={features}
          entitlements={addonFeatures}
          isLoading={entitlementsLoading}
          onUpsert={(input) => upsertAddonFeature.mutate(input)}
          onRemove={(featureId) => deleteAddonFeature.mutate(featureId)}
          onClose={() => setEntitlementsTarget(null)}
        />
      )}

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Delete addon?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Addon <span className="font-mono font-medium">{deleteTarget?.id}</span> will be
              permanently deleted. This will fail if any subscription has it attached, or if it still has entitlements — remove those first via Manage Entitlements.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setDeleteTarget(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deleteAddon.mutate(deleteTarget.id)
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
