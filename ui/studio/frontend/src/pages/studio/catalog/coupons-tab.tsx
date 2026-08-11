import { useState } from "react"
import {
  IconAlertTriangle,
  IconPencil,
  IconPlus,
  IconRefresh,
  IconTag,
  IconTrash,
} from "@tabler/icons-react"
import type {
  Coupon,
  CouponInput,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useAddCouponTarget,
  useCoupons,
  useCouponTargets,
  useCreateCoupon,
  useDeleteCoupon,
  useRemoveCouponTarget,
  useUpdateCoupon,
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
import { Textarea } from "@/components/ui/textarea"
import {
  WriteBanner,
  TableSkeleton,
  FieldHint,
  PriceUnitHint,
} from "../components/table-helpers"
import { formatJSON, toDatetimeLocalValue } from "../components/table-utils"
import { SaveConfirmDialog } from "./save-confirm-dialog"
import { formatMoney } from "./format-money"

const EMPTY_COUPON: CouponInput = {
  code: "",
  name: "",
  discount_type: "fixed",
  amount_cents: 0,
  percent_off: null,
  currency: "USD",
  cadence: "once",
  duration_count: null,
  valid_from: "",
  valid_until: "",
  max_redemptions: null,
  metadata: "{}",
  active: true,
}

interface CouponFormProps {
  coupon?: Coupon
  projectId: string
  onClose: () => void
}

function CouponForm({ coupon, projectId, onClose }: CouponFormProps) {
  const isEdit = !!coupon
  const create = useCreateCoupon(projectId)
  const update = useUpdateCoupon(projectId)
  const pending = create.isPending || update.isPending
  const { data: currencies } = useCurrencies(projectId)

  const [form, setForm] = useState<CouponInput>(
    coupon
      ? {
          code: coupon.code,
          name: coupon.name,
          discount_type: coupon.discount_type,
          amount_cents: coupon.amount_cents,
          percent_off: coupon.percent_off,
          currency: coupon.currency,
          cadence: coupon.cadence,
          duration_count: coupon.duration_count,
          valid_from: coupon.valid_from,
          valid_until: coupon.valid_until,
          max_redemptions: coupon.max_redemptions,
          metadata: coupon.metadata,
          active: coupon.active,
        }
      : EMPTY_COUPON
  )

  const set = <K extends keyof CouponInput>(k: K, v: CouponInput[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const [confirmOpen, setConfirmOpen] = useState(false)

  const initialForm: CouponInput = coupon
    ? {
        code: coupon.code,
        name: coupon.name,
        discount_type: coupon.discount_type,
        amount_cents: coupon.amount_cents,
        percent_off: coupon.percent_off,
        currency: coupon.currency,
        cadence: coupon.cadence,
        duration_count: coupon.duration_count,
        valid_from: coupon.valid_from,
        valid_until: coupon.valid_until,
        max_redemptions: coupon.max_redemptions,
        metadata: coupon.metadata,
        active: coupon.active,
      }
    : EMPTY_COUPON
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  const isValid =
    form.code.trim() !== "" &&
    form.name.trim() !== "" &&
    (form.discount_type !== "fixed" ||
      (form.amount_cents !== null && form.currency.trim() !== "")) &&
    (form.discount_type !== "percent" || form.percent_off !== null) &&
    (form.cadence !== "repeated" || form.duration_count !== null)
  const submitDisabled = pending || !isValid || (isEdit && !isDirty)

  const handleSubmit = () => {
    setConfirmOpen(false)
    if (isEdit) {
      update.mutate({ code: coupon!.code, input: form }, { onSuccess: onClose })
    } else {
      create.mutate(form, { onSuccess: onClose })
    }
  }

  return (
    <>
      <Sheet
        open
        onOpenChange={(o) => {
          if (!o) onClose()
        }}
      >
        <SheetContent className="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-2xl">
          <SheetHeader className="pb-4">
            <SheetTitle>
              {isEdit ? `Edit coupon — ${coupon!.code}` : "New coupon"}
            </SheetTitle>
            <SheetDescription>
              Changes are written directly to the production database.
            </SheetDescription>
          </SheetHeader>

          <div className="mx-6 flex flex-col gap-4 py-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="coupon-name">Name *</Label>
              <Input
                id="coupon-name"
                placeholder="Summer sale"
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
              />
              <FieldHint>
                Internal display name — not shown to the customer redeeming the
                code.
              </FieldHint>
            </div>

            {!isEdit && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="coupon-code">Code *</Label>
                <Input
                  id="coupon-code"
                  placeholder="SUMMER20"
                  value={form.code}
                  onChange={(e) => set("code", e.target.value)}
                />
                <FieldHint>
                  The code the customer enters to redeem this coupon. Cannot be
                  changed after creation.
                </FieldHint>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <Label>Discount type *</Label>
              <Select
                value={form.discount_type}
                onValueChange={(v) => {
                  const discountType = v ?? "fixed"
                  // The two fields are mutually exclusive at the DB level (CHECK
                  // constraint) — switching type must null out the other one,
                  // otherwise a leftover value (e.g. amount_cents defaulting to 0)
                  // trips the constraint on save.
                  setForm((f) => ({
                    ...f,
                    discount_type: discountType,
                    amount_cents:
                      discountType === "fixed" ? (f.amount_cents ?? 0) : null,
                    percent_off:
                      discountType === "percent" ? (f.percent_off ?? 0) : null,
                  }))
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="fixed">Fixed amount</SelectItem>
                  <SelectItem value="percent">Percentage</SelectItem>
                </SelectContent>
              </Select>
              <FieldHint>
                Fixed = a flat amount off in a specific currency. Percent = a
                percentage off the invoice total.
              </FieldHint>
            </div>

            {form.discount_type === "fixed" ? (
              <div className="grid grid-cols-2 gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="coupon-amount">Amount off</Label>
                  <Input
                    id="coupon-amount"
                    type="number"
                    value={form.amount_cents ?? 0}
                    onChange={(e) =>
                      set("amount_cents", Number(e.target.value))
                    }
                  />
                  <PriceUnitHint />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="coupon-currency">Currency</Label>
                  <Input
                    id="coupon-currency"
                    placeholder="USD"
                    value={form.currency}
                    onChange={(e) => set("currency", e.target.value)}
                  />
                  <FieldHint>
                    Must match a currency code the customer can be billed in.
                  </FieldHint>
                </div>
              </div>
            ) : (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="coupon-percent">Percent off (0–100)</Label>
                <Input
                  id="coupon-percent"
                  type="number"
                  min={0}
                  max={100}
                  value={form.percent_off ?? 0}
                  onChange={(e) => set("percent_off", Number(e.target.value))}
                />
                <FieldHint>
                  Percentage discount applied to the invoice subtotal.
                </FieldHint>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <Label>Cadence *</Label>
              <Select
                value={form.cadence}
                onValueChange={(v) => set("cadence", v ?? "once")}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="once">Once</SelectItem>
                  <SelectItem value="repeated">Repeated</SelectItem>
                  <SelectItem value="forever">Forever</SelectItem>
                </SelectContent>
              </Select>
              <FieldHint>
                <strong>Once</strong> — applies to the next invoice only.{" "}
                <strong>Repeated</strong> — applies for a fixed number of
                billing cycles, then reverts to full price.{" "}
                <strong>Forever</strong> — applies to every invoice for the life
                of the subscription.
              </FieldHint>
            </div>

            {form.cadence === "repeated" && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="coupon-duration">
                  Number of billing cycles
                </Label>
                <Input
                  id="coupon-duration"
                  type="number"
                  value={form.duration_count ?? 1}
                  onChange={(e) =>
                    set("duration_count", Number(e.target.value))
                  }
                />
                <FieldHint>
                  How many invoices in a row get the discount before the
                  subscriber pays full price.
                </FieldHint>
              </div>
            )}

            <div className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="coupon-from">Redeem after (optional)</Label>
                <Input
                  id="coupon-from"
                  type="datetime-local"
                  value={toDatetimeLocalValue(form.valid_from)}
                  onChange={(e) =>
                    set(
                      "valid_from",
                      e.target.value
                        ? new Date(e.target.value).toISOString()
                        : ""
                    )
                  }
                />
                <FieldHint>
                  When the coupon becomes redeemable. Leave blank for no
                  restriction.
                </FieldHint>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="coupon-until">Redeem before (optional)</Label>
                <Input
                  id="coupon-until"
                  type="datetime-local"
                  value={toDatetimeLocalValue(form.valid_until)}
                  onChange={(e) =>
                    set(
                      "valid_until",
                      e.target.value
                        ? new Date(e.target.value).toISOString()
                        : ""
                    )
                  }
                />
                <FieldHint>
                  When the coupon expires. Leave blank for no restriction.
                </FieldHint>
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="coupon-max">Max redemptions (optional)</Label>
              <Input
                id="coupon-max"
                type="number"
                placeholder="Unlimited"
                value={form.max_redemptions ?? ""}
                onChange={(e) =>
                  set(
                    "max_redemptions",
                    e.target.value === "" ? null : Number(e.target.value)
                  )
                }
              />
              <FieldHint>
                Total redemptions allowed across all customers combined. Leave
                blank for unlimited.
              </FieldHint>
            </div>

            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="coupon-metadata">
                  Metadata (optional JSON)
                </Label>
                <button
                  type="button"
                  onClick={() => set("metadata", formatJSON(form.metadata))}
                  className="text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  Format
                </button>
              </div>
              <Textarea
                id="coupon-metadata"
                rows={3}
                value={form.metadata}
                onChange={(e) => set("metadata", e.target.value)}
                className="font-mono text-xs"
                placeholder="{}"
              />
              <FieldHint>
                Free-form JSON for your own tracking (e.g. campaign source) —
                not used by billing logic.
              </FieldHint>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label className="flex cursor-pointer items-center gap-2">
                <input
                  type="checkbox"
                  checked={form.active}
                  onChange={(e) => set("active", e.target.checked)}
                  className="rounded"
                />
                Active
              </Label>
              <FieldHint>
                Inactive coupons can't be redeemed, but past redemptions are
                preserved.
              </FieldHint>
            </div>
          </div>

          <SheetFooter className="mt-auto pt-4">
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              onClick={() => setConfirmOpen(true)}
              disabled={submitDisabled}
            >
              {pending ? "Saving…" : isEdit ? "Save changes" : "Create coupon"}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {confirmOpen && (
        <SaveConfirmDialog
          title={
            isEdit ? `Confirm changes — ${coupon!.code}` : "Confirm new coupon"
          }
          fields={[
            {
              label: "Code",
              value: (
                <code className="font-mono">
                  {isEdit ? coupon!.code : form.code}
                </code>
              ),
            },
            { label: "Name", value: form.name },
            {
              label: "Discount",
              value:
                form.discount_type === "fixed"
                  ? `${formatMoney(form.amount_cents, form.currency, currencies)} off`
                  : `${form.percent_off ?? 0}% off`,
            },
            {
              label: "Cadence",
              value:
                form.cadence === "repeated"
                  ? `Repeated — ${form.duration_count ?? 0} cycle(s)`
                  : form.cadence,
            },
            {
              label: "Redeem window",
              value: `${form.valid_from || "no start"} → ${form.valid_until || "no end"}`,
            },
            {
              label: "Max redemptions",
              value: form.max_redemptions ?? "Unlimited",
            },
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

function CouponTargetsSheet({
  coupon,
  projectId,
  onClose,
}: {
  coupon: Coupon
  projectId: string
  onClose: () => void
}) {
  const { data: targets, isLoading } = useCouponTargets(projectId, coupon.code)
  const addTarget = useAddCouponTarget(projectId, coupon.code)
  const removeTarget = useRemoveCouponTarget(projectId, coupon.code)

  const [subjectType, setSubjectType] = useState("organization")
  const [subjectId, setSubjectId] = useState("")

  const handleAdd = () => {
    if (!subjectId) return
    addTarget.mutate({ subjectType, subjectId })
    setSubjectId("")
  }

  return (
    <Sheet
      open
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <SheetContent className="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-xl">
        <SheetHeader className="pb-4">
          <SheetTitle>Targets — {coupon.code}</SheetTitle>
          <SheetDescription>
            No targets = redeemable by anyone. Add a target to restrict this
            coupon to specific organizations or users.
          </SheetDescription>
        </SheetHeader>

        <div className="mx-6 flex flex-col gap-4 py-4">
          {isLoading ? (
            <TableSkeleton cols={3} />
          ) : !targets || targets.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted-foreground">
              Open to everyone — no targets set
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Type</TableHead>
                  <TableHead>ID</TableHead>
                  <TableHead className="w-14 text-right">—</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {targets.map((t) => (
                  <TableRow key={`${t.subject_type}-${t.subject_id}`}>
                    <TableCell className="text-sm capitalize">
                      {t.subject_type}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {t.subject_id}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                        onClick={() =>
                          removeTarget.mutate({
                            subjectType: t.subject_type,
                            subjectId: t.subject_id,
                          })
                        }
                      >
                        <IconTrash className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          <div className="flex flex-col gap-2 border-t pt-4">
            <Label>Add target</Label>
            <Select
              value={subjectType}
              onValueChange={(v) => setSubjectType(v ?? "organization")}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="organization">Organization</SelectItem>
                <SelectItem value="user">User</SelectItem>
              </SelectContent>
            </Select>
            <Input
              placeholder={
                subjectType === "organization"
                  ? "Organization UUID"
                  : "User auth_sub"
              }
              value={subjectId}
              onChange={(e) => setSubjectId(e.target.value)}
            />
            <FieldHint>
              {subjectType === "organization"
                ? "The organization's UUID (organization.organizations.id) — only members of this organization can redeem the code."
                : "The user's auth_sub (account.users.auth_sub) — only this specific user can redeem the code."}
            </FieldHint>
            <Button size="sm" onClick={handleAdd} disabled={!subjectId}>
              <IconPlus className="size-4" />
              Add target
            </Button>
          </div>
        </div>

        <SheetFooter className="mt-auto pt-4">
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export function CouponsTab({ projectId }: { projectId: string }) {
  const {
    data: coupons,
    isLoading,
    error,
    refetch,
    isFetching,
  } = useCoupons(projectId)
  const deleteCoupon = useDeleteCoupon(projectId)

  const [formTarget, setFormTarget] = useState<Coupon | null | "new">(null)
  const [deleteTarget, setDeleteTarget] = useState<Coupon | null>(null)
  const [targetsTarget, setTargetsTarget] = useState<Coupon | null>(null)

  return (
    <div className="flex flex-col gap-4">
      <WriteBanner />

      <div className="flex items-center justify-between">
        <Button
          variant="outline"
          size="sm"
          onClick={() => refetch()}
          disabled={isFetching}
        >
          <IconRefresh
            className={`size-4 ${isFetching ? "animate-spin" : ""}`}
          />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setFormTarget("new")}>
          <IconPlus className="size-4" />
          Add coupon
        </Button>
      </div>

      {isLoading ? (
        <TableSkeleton cols={6} />
      ) : error ? (
        <p className="text-sm text-destructive">{String(error)}</p>
      ) : !coupons || coupons.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">
          No coupons found
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Code</TableHead>
              <TableHead>Name</TableHead>
              <TableHead className="text-center">Discount</TableHead>
              <TableHead className="text-center">Cadence</TableHead>
              <TableHead className="text-center">Redeemed</TableHead>
              <TableHead className="w-20 text-center">Status</TableHead>
              <TableHead className="w-36 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {coupons.map((c: Coupon) => (
              <TableRow key={c.code}>
                <TableCell>
                  <code className="font-mono text-xs">{c.code}</code>
                </TableCell>
                <TableCell className="text-sm font-medium">{c.name}</TableCell>
                <TableCell className="text-center text-xs">
                  {c.discount_type === "fixed"
                    ? `${((c.amount_cents ?? 0) / 100).toFixed(2)} ${c.currency}`
                    : `${c.percent_off}%`}
                </TableCell>
                <TableCell className="text-center text-xs capitalize">
                  {c.cadence}
                </TableCell>
                <TableCell className="text-center text-xs tabular-nums">
                  {c.redeemed_count}/{c.max_redemptions ?? "∞"}
                </TableCell>
                <TableCell className="text-center">
                  <Badge
                    variant={c.active ? "default" : "secondary"}
                    className="text-xs"
                  >
                    {c.active ? "active" : "inactive"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0"
                      onClick={() => setTargetsTarget(c)}
                      title="Manage targets"
                    >
                      <IconTag className="size-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0"
                      onClick={() => setFormTarget(c)}
                    >
                      <IconPencil className="size-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                      onClick={() => setDeleteTarget(c)}
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
        <CouponForm
          coupon={formTarget === "new" ? undefined : formTarget}
          projectId={projectId}
          onClose={() => setFormTarget(null)}
        />
      )}

      {targetsTarget && (
        <CouponTargetsSheet
          coupon={targetsTarget}
          projectId={projectId}
          onClose={() => setTargetsTarget(null)}
        />
      )}

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Delete coupon?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Coupon{" "}
              <span className="font-mono font-medium">
                {deleteTarget?.code}
              </span>{" "}
              will be permanently deleted. This will fail if it has already been
              redeemed — deactivate it instead.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setDeleteTarget(null)}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deleteCoupon.mutate(deleteTarget.code)
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
