import { useState } from "react"
import { IconAlertTriangle, IconPencil, IconPlus, IconRefresh, IconTrash } from "@tabler/icons-react"
import type {
  Currency,
  CurrencyInput,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useCurrencies,
  useCreateCurrency,
  useDeleteCurrency,
  useUpdateCurrency,
} from "@/hooks/use-references"
import { WriteBanner, TableSkeleton } from "../components/table-helpers"
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

const EMPTY_CURRENCY: CurrencyInput = {
  code: "",
  name: "",
  symbol: "",
  decimal_places: 2,
  active: true,
}

interface CurrencyFormProps {
  currency?: Currency
  projectId: string
  onClose: () => void
}

function CurrencyForm({ currency, projectId, onClose }: CurrencyFormProps) {
  const isEdit = !!currency
  const create = useCreateCurrency(projectId)
  const update = useUpdateCurrency(projectId)
  const pending = create.isPending || update.isPending

  const [form, setForm] = useState<CurrencyInput>(
    currency
      ? {
          code: currency.code,
          name: currency.name,
          symbol: currency.symbol,
          decimal_places: currency.decimal_places,
          active: currency.active,
        }
      : EMPTY_CURRENCY,
  )

  const set = <K extends keyof CurrencyInput>(k: K, v: CurrencyInput[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const handleSubmit = () => {
    if (isEdit) {
      update.mutate({ code: currency!.code, input: form }, { onSuccess: onClose })
    } else {
      create.mutate(form, { onSuccess: onClose })
    }
  }

  return (
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-lg overflow-y-auto">
        <SheetHeader className="pb-4">
          <SheetTitle>{isEdit ? `Edit currency — ${currency!.code}` : "New currency"}</SheetTitle>
          <SheetDescription>Changes are written directly to the production database.</SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          {!isEdit && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cur-code">ISO code *</Label>
              <Input
                id="cur-code"
                placeholder="SGD"
                maxLength={3}
                className="uppercase"
                value={form.code}
                onChange={(e) => set("code", e.target.value.toUpperCase())}
              />
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cur-name">Name *</Label>
            <Input
              id="cur-name"
              placeholder="Singapore Dollar"
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cur-symbol">Symbol</Label>
              <Input
                id="cur-symbol"
                placeholder="S$"
                value={form.symbol}
                onChange={(e) => set("symbol", e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cur-decimals">Decimal places</Label>
              <Input
                id="cur-decimals"
                type="number"
                min={0}
                max={4}
                value={form.decimal_places}
                onChange={(e) => set("decimal_places", Number(e.target.value))}
              />
            </div>
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
          </div>
        </div>

        <SheetFooter className="pt-4 mt-auto">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={handleSubmit} disabled={pending}>
            {pending ? "Saving…" : isEdit ? "Save changes" : "Create currency"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export function CurrenciesTab({ projectId }: { projectId: string }) {
  const { data: currencies, isLoading, error, refetch, isFetching } = useCurrencies(projectId)
  const deleteCurrency = useDeleteCurrency(projectId)

  const [formTarget, setFormTarget] = useState<Currency | null | "new">(null)
  const [deleteTarget, setDeleteTarget] = useState<Currency | null>(null)

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
          Add currency
        </Button>
      </div>

      {isLoading ? (
        <TableSkeleton cols={5} />
      ) : error ? (
        <p className="text-sm text-destructive">{String(error)}</p>
      ) : !currencies || currencies.length === 0 ? (
        <p className="text-sm text-muted-foreground py-8 text-center">No currencies found</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-16">Code</TableHead>
              <TableHead>Name</TableHead>
              <TableHead className="w-20">Symbol</TableHead>
              <TableHead className="w-28">Decimal places</TableHead>
              <TableHead className="w-20 text-center">Status</TableHead>
              <TableHead className="w-28 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {currencies.map((c: Currency) => (
              <TableRow key={c.code}>
                <TableCell><code className="font-mono text-xs">{c.code}</code></TableCell>
                <TableCell className="text-sm">{c.name}</TableCell>
                <TableCell className="text-sm font-medium">{c.symbol}</TableCell>
                <TableCell className="text-xs tabular-nums text-center">{c.decimal_places}</TableCell>
                <TableCell className="text-center">
                  <Badge variant={c.active ? "default" : "secondary"} className="text-xs">
                    {c.active ? "active" : "inactive"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1.5">
                    <Button
                      variant="ghost" size="sm" className="h-7 w-7 p-0"
                      onClick={() => setFormTarget(c)}
                    >
                      <IconPencil className="size-3.5" />
                    </Button>
                    <Button
                      variant="ghost" size="sm" className="h-7 w-7 p-0 text-destructive hover:text-destructive"
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
        <CurrencyForm
          currency={formTarget === "new" ? undefined : formTarget}
          projectId={projectId}
          onClose={() => setFormTarget(null)}
        />
      )}

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Delete currency?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Currency <span className="font-mono font-medium">{deleteTarget?.code}</span> will be
              permanently deleted. This will fail if any active subscription uses this currency.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setDeleteTarget(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deleteCurrency.mutate(deleteTarget.code)
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
