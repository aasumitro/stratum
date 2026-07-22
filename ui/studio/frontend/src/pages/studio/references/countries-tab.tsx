import { useState } from "react"
import { IconAlertTriangle, IconPencil, IconPlus, IconRefresh, IconSearch, IconTrash } from "@tabler/icons-react"
import type {
  Country,
  CountryInput,
} from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useCountries,
  useCurrencies,
  useCreateCountry,
  useDeleteCountry,
  useUpdateCountry,
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

const EMPTY_COUNTRY: CountryInput = {
  code: "",
  name: "",
  phone_code: "",
  currency_code: "",
  tax_rate_bps: 0,
  active: true,
}

interface CountryFormProps {
  country?: Country
  projectId: string
  onClose: () => void
}

function CountryForm({ country, projectId, onClose }: CountryFormProps) {
  const isEdit = !!country
  const create = useCreateCountry(projectId)
  const update = useUpdateCountry(projectId)
  const { data: currencies } = useCurrencies(projectId)
  const pending = create.isPending || update.isPending

  const [form, setForm] = useState<CountryInput>(
    country
      ? {
          code: country.code,
          name: country.name,
          phone_code: country.phone_code,
          currency_code: country.currency_code,
          tax_rate_bps: country.tax_rate_bps,
          active: country.active,
        }
      : EMPTY_COUNTRY,
  )

  const set = <K extends keyof CountryInput>(k: K, v: CountryInput[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const handleSubmit = () => {
    if (isEdit) {
      update.mutate({ code: country!.code, input: form }, { onSuccess: onClose })
    } else {
      create.mutate(form, { onSuccess: onClose })
    }
  }

  return (
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent className="flex flex-col gap-0 w-full sm:max-w-lg overflow-y-auto">
        <SheetHeader className="pb-4">
          <SheetTitle>{isEdit ? `Edit country — ${country!.code}` : "New country"}</SheetTitle>
          <SheetDescription>Changes are written directly to the production database.</SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 py-4 mx-6">
          {!isEdit && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="country-code">ISO code *</Label>
              <Input
                id="country-code"
                placeholder="SG"
                maxLength={3}
                className="uppercase"
                value={form.code}
                onChange={(e) => set("code", e.target.value.toUpperCase())}
              />
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="country-name">Name *</Label>
            <Input
              id="country-name"
              placeholder="Singapore"
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="country-phone">Phone code</Label>
              <Input
                id="country-phone"
                placeholder="+65"
                value={form.phone_code}
                onChange={(e) => set("phone_code", e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="country-currency">Currency code</Label>
              <select
                id="country-currency"
                value={form.currency_code}
                onChange={(e) => set("currency_code", e.target.value)}
                className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              >
                <option value="">— select —</option>
                {(currencies ?? []).map((c) => (
                  <option key={c.code} value={c.code}>
                    {c.code} — {c.name}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="country-tax">Tax rate (bps)</Label>
              <Input
                id="country-tax"
                type="number"
                min={0}
                value={form.tax_rate_bps}
                onChange={(e) => set("tax_rate_bps", Number(e.target.value))}
              />
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
            </div>
          </div>
        </div>

        <SheetFooter className="pt-4 mt-auto">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={handleSubmit} disabled={pending}>
            {pending ? "Saving…" : isEdit ? "Save changes" : "Create country"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export function CountriesTab({ projectId }: { projectId: string }) {
  const { data: countries, isLoading, error, refetch, isFetching } = useCountries(projectId)
  const deleteCountry = useDeleteCountry(projectId)

  const [formTarget, setFormTarget] = useState<Country | null | "new">(null)
  const [deleteTarget, setDeleteTarget] = useState<Country | null>(null)
  const [search, setSearch] = useState("")

  const filtered = search.trim()
    ? (countries ?? []).filter(
        (c) =>
          c.code.toLowerCase().includes(search.toLowerCase()) ||
          c.name.toLowerCase().includes(search.toLowerCase()),
      )
    : (countries ?? [])

  return (
    <div className="flex flex-col gap-4">
      <WriteBanner />

      <div className="flex items-center gap-3">
        <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
          <IconRefresh className={`size-4 ${isFetching ? "animate-spin" : ""}`} />
          Refresh
        </Button>
        <div className="relative flex-1 max-w-sm">
          <IconSearch className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted-foreground pointer-events-none" />
          <Input
            className="pl-8"
            placeholder="Search by code or name…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <Button size="sm" className="ml-auto" onClick={() => setFormTarget("new")}>
          <IconPlus className="size-4" />
          Add country
        </Button>
      </div>

      {isLoading ? (
        <TableSkeleton cols={6} />
      ) : error ? (
        <p className="text-sm text-destructive">{String(error)}</p>
      ) : !countries || countries.length === 0 ? (
        <p className="text-sm text-muted-foreground py-8 text-center">No countries found</p>
      ) : filtered.length === 0 ? (
        <p className="text-sm text-muted-foreground py-8 text-center">No countries match “{search}”</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-16">Code</TableHead>
              <TableHead>Name</TableHead>
              <TableHead className="w-24">Phone</TableHead>
              <TableHead className="w-24">Currency</TableHead>
              <TableHead className="w-28">Tax (bps)</TableHead>
              <TableHead className="w-20 text-center">Status</TableHead>
              <TableHead className="w-28 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((c: Country) => (
              <TableRow key={c.code}>
                <TableCell><code className="font-mono text-xs">{c.code}</code></TableCell>
                <TableCell className="text-sm">{c.name}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{c.phone_code}</TableCell>
                <TableCell><code className="font-mono text-xs">{c.currency_code}</code></TableCell>
                <TableCell className="text-xs tabular-nums">{c.tax_rate_bps}</TableCell>
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
        <CountryForm
          country={formTarget === "new" ? undefined : formTarget}
          projectId={projectId}
          onClose={() => setFormTarget(null)}
        />
      )}

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-destructive" />
              Delete country?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Country <span className="font-mono font-medium">{deleteTarget?.code}</span> will be
              permanently deleted. This will fail if any organization uses this country.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setDeleteTarget(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => {
                if (deleteTarget) {
                  deleteCountry.mutate(deleteTarget.code)
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
