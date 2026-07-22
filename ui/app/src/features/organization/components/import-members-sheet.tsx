import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconUpload, IconDownload } from "@tabler/icons-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import {
  useImportMembers,
  type ImportRow,
  type ImportRowResult,
} from "@/features/organization/hooks"
import type { OrganizationRole } from "@/types/organization"

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (v: boolean) => void
}

const CSV_TEMPLATE = "email,role\nsara@beta.io,member\ntom@acme.com,admin"

function parseCSV(text: string): ImportRow[] {
  const lines = text
    .trim()
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean)
  const start = lines[0]?.toLowerCase().includes("email") ? 1 : 0
  return lines
    .slice(start)
    .map((line) => {
      const [email, role] = line.split(",").map((s) => s.trim())
      return {
        email: email ?? "",
        role: (role || "member") as OrganizationRole,
      }
    })
    .filter((row) => row.email)
}

// Dry-run preview before commit — valid rows importable even with some bad
// rows alongside them.
export function ImportMembersSheet({
  organizationId,
  open,
  onOpenChange,
}: Props) {
  const { t } = useTranslation()
  const { mutate: importMembers, isPending } = useImportMembers(organizationId)
  const [raw, setRaw] = useState("")
  const [rows, setRows] = useState<ImportRow[]>([])
  const [preview, setPreview] = useState<{
    validCount: number
    errors: ImportRowResult[]
  } | null>(null)

  function reset() {
    setRaw("")
    setRows([])
    setPreview(null)
  }

  function downloadTemplate() {
    const blob = new Blob([CSV_TEMPLATE], { type: "text/csv" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = "members-template.csv"
    a.click()
    URL.revokeObjectURL(url)
  }

  function downloadErrorRows() {
    if (!preview) return
    const lines = [
      "email,role,reason",
      ...preview.errors.map((e) => `${e.email},,${e.reason}`),
    ]
    const blob = new Blob([lines.join("\n")], { type: "text/csv" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = "import-errors.csv"
    a.click()
    URL.revokeObjectURL(url)
  }

  function runDryRun() {
    const parsed = parseCSV(raw)
    if (!parsed.length) return
    setRows(parsed)
    importMembers(
      { rows: parsed, dry_run: true },
      {
        onSuccess: (res) => {
          if (!res.data) return
          setPreview({
            validCount: res.data.valid_count ?? 0,
            errors: res.data.errors ?? [],
          })
        },
      }
    )
  }

  function commitImport() {
    if (!preview) return
    const errorIndexes = new Set(preview.errors.map((e) => e.index))
    const validRows = rows.filter((_, i) => !errorIndexes.has(i))
    importMembers(
      { rows: validRows, dry_run: false },
      {
        onSuccess: (res) => {
          toast.success(
            t("organization.members.importResult", {
              count: res.data?.imported ?? 0,
            })
          )
          reset()
          onOpenChange(false)
        },
      }
    )
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        if (!v) reset()
        onOpenChange(v)
      }}
    >
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{t("organization.members.importTitle")}</SheetTitle>
          <SheetDescription>
            {t("organization.members.importDescription")}
          </SheetDescription>
        </SheetHeader>

        <div className="mx-4 mt-6 flex flex-col gap-4">
          <Button
            variant="outline"
            size="sm"
            onClick={downloadTemplate}
            className="self-start"
          >
            <IconDownload data-icon="inline-start" />
            {t("organization.members.downloadTemplate")}
          </Button>

          <Textarea
            placeholder={t("organization.members.importPlaceholder")}
            value={raw}
            onChange={(e) => {
              setRaw(e.target.value)
              setPreview(null)
            }}
            rows={8}
            className="font-mono text-xs"
          />

          {preview && (
            <div className="flex flex-col gap-2 rounded-lg border p-3 text-sm">
              <div className="flex items-center gap-2">
                <Badge className="border-emerald-500/30 bg-emerald-500/10 text-emerald-600">
                  {t("organization.members.importValidCount", {
                    count: preview.validCount,
                  })}
                </Badge>
                {preview.errors.length > 0 && (
                  <Badge className="border-destructive/30 bg-destructive/10 text-destructive">
                    {t("organization.members.importErrorCount", {
                      count: preview.errors.length,
                    })}
                  </Badge>
                )}
              </div>
              {preview.errors.length > 0 && (
                <div className="flex flex-col gap-0.5 text-xs text-destructive">
                  {preview.errors.slice(0, 5).map((e) => (
                    <span key={e.index}>
                      {t("organization.members.importRowError", {
                        row: e.index + 1,
                        reason: e.reason,
                      })}
                    </span>
                  ))}
                  {preview.errors.length > 5 && (
                    <span className="text-muted-foreground">
                      {t("organization.members.importMoreErrors", {
                        count: preview.errors.length - 5,
                      })}
                    </span>
                  )}
                </div>
              )}
              {preview.errors.length > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={downloadErrorRows}
                  className="self-start"
                >
                  {t("organization.members.downloadErrorRows")}
                </Button>
              )}
            </div>
          )}

          {preview ? (
            <Button
              onClick={commitImport}
              disabled={isPending || preview.validCount === 0}
            >
              {isPending && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              {t("organization.members.importSubmit", {
                count: preview.validCount,
              })}
            </Button>
          ) : (
            <Button onClick={runDryRun} disabled={isPending || !raw.trim()}>
              {isPending && (
                <IconLoader2
                  data-icon="inline-start"
                  className="animate-spin"
                />
              )}
              <IconUpload data-icon="inline-start" />
              {t("organization.members.importPreview")}
            </Button>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
