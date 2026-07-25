export function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .slice(0, 63)
}

export function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function initials(name: string): string {
  return name
    .split(" ")
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase()
}

// formatMoney matches the backend's platform/pdf.FormatMoney convention:
// USD (and other 2-decimal currencies) store amount_cents/100; IDR has no
// minor unit here, so it renders the raw integer.
export function formatMoney(amountCents: number, currency: string): string {
  if (currency.toUpperCase() === "IDR") {
    return `Rp${amountCents.toLocaleString("id-ID")}`
  }
  return `$${(amountCents / 100).toLocaleString("en-US", { minimumFractionDigits: 2 })}`
}

export function timeAgo(dateStr: string): string {
  const ms = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(ms / 60000)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  const value = bytes / Math.pow(1024, i)
  return `${value % 1 === 0 ? value : value.toFixed(1)} ${units[i]}`
}
