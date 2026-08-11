import type { Currency } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatMoney } from "./format-money"

// Input form -> review the decoded/formatted values -> confirm button actually
// submits. Catches typos like an amount that's 100x off before it's saved.

function parsePriceRows(
  pricesJSON: string,
  currencies: Currency[] | null | undefined
) {
  let parsed: Record<string, { monthly?: number; yearly?: number }>
  try {
    parsed = JSON.parse(pricesJSON)
  } catch {
    return null
  }
  return Object.entries(parsed).map(([code, v]) => ({
    code,
    monthly: formatMoney(v?.monthly, code, currencies),
    yearly: formatMoney(v?.yearly, code, currencies),
  }))
}

interface SaveConfirmDialogProps {
  title: string
  fields: { label: string; value: React.ReactNode }[]
  pricesJSON?: string
  currencies?: Currency[] | null
  pending: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function SaveConfirmDialog({
  title,
  fields,
  pricesJSON,
  currencies,
  pending,
  onConfirm,
  onCancel,
}: SaveConfirmDialogProps) {
  const priceRows =
    pricesJSON !== undefined
      ? parsePriceRows(pricesJSON, currencies)
      : undefined
  const blockedByInvalidJSON = pricesJSON !== undefined && priceRows === null

  return (
    <AlertDialog
      open
      onOpenChange={(o) => {
        if (!o) onCancel()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>
            Review the details below before saving — this is the last chance to
            catch a typo (like a price that's 100x off) before it's written to
            production.
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div className="flex flex-col divide-y">
          {fields.map((f, i) => (
            <div
              key={i}
              className="flex items-start justify-between gap-4 py-2 text-sm"
            >
              <span className="shrink-0 text-muted-foreground">{f.label}</span>
              <span className="text-right font-medium break-words">
                {f.value}
              </span>
            </div>
          ))}
        </div>

        {priceRows !== undefined && (
          <div className="flex flex-col gap-1.5">
            <p className="text-xs font-medium text-muted-foreground">Prices</p>
            {priceRows === null ? (
              <p className="text-sm text-destructive">
                Prices field is not valid JSON — go back and fix it before
                saving.
              </p>
            ) : priceRows.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No currencies defined.
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Currency</TableHead>
                    <TableHead>Monthly</TableHead>
                    <TableHead>Yearly</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {priceRows.map((r) => (
                    <TableRow key={r.code}>
                      <TableCell className="font-mono text-xs">
                        {r.code}
                      </TableCell>
                      <TableCell>{r.monthly}</TableCell>
                      <TableCell>{r.yearly}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel onClick={onCancel}>Go back</AlertDialogCancel>
          <AlertDialogAction
            onClick={onConfirm}
            disabled={pending || blockedByInvalidJSON}
          >
            {pending ? "Saving…" : "Looks right, save"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
