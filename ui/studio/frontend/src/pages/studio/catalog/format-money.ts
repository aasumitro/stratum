import type { Currency } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"

export function formatMoney(
  amount: number | null | undefined,
  code: string,
  currencies: Currency[] | null | undefined
): string {
  if (amount === null || amount === undefined) return "—"
  const currency = currencies?.find((c) => c.code === code)
  const decimals = currency?.decimal_places ?? 2
  const symbol = currency?.symbol ?? (code ? `${code} ` : "")
  return `${symbol}${(amount / 10 ** decimals).toLocaleString(undefined, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  })}`
}
