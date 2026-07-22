import { useState } from "react"
import {
  IconAlertTriangle,
  IconPencil,
  IconPlus,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react"
import type {
  Feature,
  FeatureInput,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useCreateFeature,
  useDeleteFeature,
  useFeatures,
  useUpdateFeature,
} from "@/hooks/use-catalog"
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
import { WriteBanner, TableSkeleton, FieldHint } from "../components/table-helpers"
import { SaveConfirmDialog } from "./save-confirm-dialog"

const EMPTY_FEATURE: FeatureInput = {
  id: "",
  name: "",
  description: "",
  type: "boolean",
  metric_key: "",
  active: true,
}

interface FeatureFormProps {
  feature?: Feature
  projectId: string
  onClose: () => void
}

function FeatureForm({ feature, projectId, onClose }: FeatureFormProps) {
  const isEdit = !!feature
  const create = useCreateFeature(projectId)
  const update = useUpdateFeature(projectId)
  const pending = create.isPending || update.isPending

  const [form, setForm] = useState<FeatureInput>(
    feature
      ? {
          id: feature.id,
          name: feature.name,
          description: feature.description,
          type: feature.type,
          metric_key: feature.metric_key,
          active: feature.active,
        }
      : EMPTY_FEATURE,
  )

  const set = <K extends keyof FeatureInput>(k: K, v: FeatureInput[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const [confirmOpen, setConfirmOpen] = useState(false)

  const initialForm: FeatureInput = feature
    ? { id: feature.id, name: feature.name, description: feature.description, type: feature.type, metric_key: feature.metric_key, active: feature.active }
    : EMPTY_FEATURE
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  const isValid = form.id.trim() !== "" && form.name.trim() !== "" && (form.type !== "metered" || form.metric_key.trim() !== "")
  const submitDisabled = pending || !isValid || (isEdit && !isDirty)

  const handleSubmit = () => {
    setConfirmOpen(false)
    if (isEdit) {
      update.mutate({ featureID: feature!.id, input: form }, { onSuccess: onClose })
    } else {
      create.mutate(form, { onSuccess: onClose })
    }
  }

  return (
    <>
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-lg overflow-y-auto">
        <SheetHeader className="pb-4">
          <SheetTitle>{isEdit ? `Edit feature — ${feature!.id}` : "New feature"}</SheetTitle>
          <SheetDescription>
            Changes are written directly to the production database.
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          {!isEdit && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="feature-id">ID (slug) *</Label>
              <Input
                id="feature-id"
                placeholder="priority_support"
                value={form.id}
                onChange={(e) => set("id", e.target.value)}
              />
              <FieldHint>
                Unique identifier referenced by plans and addons when granting this feature. Cannot be changed after creation.
              </FieldHint>
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="feature-name">Name *</Label>
            <Input
              id="feature-name"
              placeholder="Priority Support"
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
            />
            <FieldHint>Display name shown wherever this feature's entitlement is listed.</FieldHint>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="feature-desc">Description</Label>
            <Input
              id="feature-desc"
              placeholder="Faster support response times."
              value={form.description}
              onChange={(e) => set("description", e.target.value)}
            />
            <FieldHint>
              What this feature actually grants — shown in the Features list and when picking a feature to add to a
              plan or addon, so it's easier to tell similarly-named features apart.
            </FieldHint>
          </div>

          <div className="flex flex-col gap-1.5">
            <Label>Type *</Label>
            <Select
              value={form.type}
              onValueChange={(v) => {
                const type = v ?? "boolean"
                // metric_key is only valid for metered (CHECK constraint) — clear
                // it when switching away, otherwise a leftover value from a prior
                // "metered" selection trips the constraint on save.
                setForm((f) => ({ ...f, type, metric_key: type === "metered" ? f.metric_key : "" }))
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="metered">Metered</SelectItem>
                <SelectItem value="boolean">Boolean</SelectItem>
                <SelectItem value="static">Static</SelectItem>
                <SelectItem value="config">Config</SelectItem>
              </SelectContent>
            </Select>
            <FieldHint>
              <strong>Metered</strong> — tracked usage with a numeric limit (e.g. members, storage).{" "}
              <strong>Boolean</strong> — simple on/off access, no value.{" "}
              <strong>Static</strong> — a fixed label, no value (e.g. "SLA guarantee").{" "}
              <strong>Config</strong> — delivers a JSON value to the app (e.g. a rate-limit tier).
            </FieldHint>
          </div>

          {form.type === "metered" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="feature-metric">Metric key *</Label>
              <Input
                id="feature-metric"
                placeholder="storage_bytes"
                value={form.metric_key}
                onChange={(e) => set("metric_key", e.target.value)}
              />
              <FieldHint>
                Must match the metric name recorded in <code className="font-mono">billing.usage</code> (e.g. <code className="font-mono">storage_bytes</code>) — this
                is how actual usage gets compared against the limit set on a plan or addon.
              </FieldHint>
            </div>
          )}

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
            <FieldHint>Inactive features can't be added to new entitlements, but existing ones keep working.</FieldHint>
          </div>
        </div>

        <SheetFooter className="pt-4 mt-auto">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => setConfirmOpen(true)} disabled={submitDisabled}>
            {pending ? "Saving…" : isEdit ? "Save changes" : "Create feature"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>

    {confirmOpen && (
      <SaveConfirmDialog
        title={isEdit ? `Confirm changes — ${feature!.id}` : "Confirm new feature"}
        fields={[
          { label: "ID", value: <code className="font-mono">{isEdit ? feature!.id : form.id}</code> },
          { label: "Name", value: form.name },
          { label: "Description", value: form.description || "—" },
          { label: "Type", value: form.type },
          ...(form.type === "metered" ? [{ label: "Metric key", value: <code className="font-mono">{form.metric_key || "—"}</code> }] : []),
          { label: "Active", value: form.active ? "Yes" : "No" },
        ]}
        pending={pending}
        onConfirm={handleSubmit}
        onCancel={() => setConfirmOpen(false)}
      />
    )}
    </>
  )
}

export function FeaturesTab({ projectId }: { projectId: string }) {
  const { data: features, isLoading, error, refetch, isFetching } = useFeatures(projectId)
  const deleteFeature = useDeleteFeature(projectId)

  const [formTarget, setFormTarget] = useState<Feature | null | "new">(null)
  const [deleteTarget, setDeleteTarget] = useState<Feature | null>(null)

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
          Add feature
        </Button>
      </div>

      {isLoading ? (
        <TableSkeleton cols={5} />
      ) : error ? (
        <p className="text-sm text-destructive">{String(error)}</p>
      ) : !features || features.length === 0 ? (
        <p className="text-sm text-muted-foreground py-8 text-center">No features found</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Description</TableHead>
              <TableHead className="w-28 text-center">Type</TableHead>
              <TableHead className="w-20 text-center">Status</TableHead>
              <TableHead className="w-24 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {features.map((f: Feature) => (
              <TableRow key={f.id}>
                <TableCell><code className="font-mono text-xs">{f.id}</code></TableCell>
                <TableCell className="font-medium text-sm">{f.name}</TableCell>
                <TableCell className="text-xs text-muted-foreground max-w-xs truncate">{f.description || "—"}</TableCell>
                <TableCell className="text-center">
                  <Badge variant="outline" className="text-xs">{f.type}</Badge>
                </TableCell>
                <TableCell className="text-center">
                  <Badge variant={f.active ? "default" : "secondary"} className="text-xs">
                    {f.active ? "active" : "inactive"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1.5">
                    <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={() => setFormTarget(f)}>
                      <IconPencil className="size-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                      onClick={() => setDeleteTarget(f)}
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
        <FeatureForm
          feature={formTarget === "new" ? undefined : formTarget}
          projectId={projectId}
          onClose={() => setFormTarget(null)}
        />
      )}

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Delete feature?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Feature <span className="font-mono font-medium">{deleteTarget?.id}</span> will be
              permanently deleted. This will fail if any plan or addon still uses it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setDeleteTarget(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deleteFeature.mutate(deleteTarget.id)
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
