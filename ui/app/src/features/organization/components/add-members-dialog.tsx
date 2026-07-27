import { useState, useRef, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconUpload, IconDownload } from "@tabler/icons-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import { InviteCodeSection } from "@/features/organization/components/invite-code-section"
import {
  useInvite,
  useImportMembers,
  type ImportRow,
  type ImportRowResult,
} from "@/features/organization/hooks"
import { useBillingFeatures } from "@/features/billing/hooks"
import type { OrganizationRole } from "@/types/organization"

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (v: boolean) => void
  isOwner: boolean
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const CSV_TEMPLATE = "email,role\nsara@beta.io,member\ntom@acme.com,admin"

function parseEmails(raw: string): { valid: string[]; invalid: string[] } {
  const tokens = raw
    .split(/[\n,]/)
    .map((t) => t.trim())
    .filter(Boolean)
  const valid: string[] = []
  const invalid: string[] = []
  for (const t of tokens) (EMAIL_RE.test(t) ? valid : invalid).push(t)
  return { valid, invalid }
}

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
      const parsedRole = (role || "member").toLowerCase().trim()
      return {
        email: email ?? "",
        role: parsedRole as OrganizationRole,
      }
    })
    .filter((row) => row.email)
}

export function AddMembersDialog({
  organizationId,
  open,
  onOpenChange,
  isOwner,
}: Props) {
  const { t } = useTranslation()

  // 1. Email Invite Hook
  const { mutate: invite, isPending: invitePending } = useInvite(organizationId)
  const { data: featuresData } = useBillingFeatures(organizationId)
  const membersFeature = (featuresData?.data ?? []).find(
    (f) => f.feature_id === "members"
  )
  const seatsLeft =
    membersFeature?.remaining != null && membersFeature.remaining >= 0
      ? membersFeature.remaining
      : null
  const atLimit = seatsLeft === 0

  const [emailsRaw, setEmailsRaw] = useState("")
  const { valid, invalid } = parseEmails(emailsRaw)

  const form = useForm({
    defaultValues: { role: "member" as OrganizationRole },
    onSubmit: async ({ value }) => {
      if (valid.length === 0) return
      await Promise.all(
        valid.map(
          (email) =>
            new Promise<void>((resolve) => {
              invite(
                { email, role: value.role },
                { onSuccess: () => resolve(), onError: () => resolve() }
              )
            })
        )
      )
      setEmailsRaw("")
      onOpenChange(false)
    },
  })

  // 2. CSV Import Hooks
  const { mutate: importMembers, isPending: importPending } =
    useImportMembers(organizationId)
  const [csvRaw, setCsvRaw] = useState("")
  const [csvRows, setCsvRows] = useState<ImportRow[]>([])
  const [csvPreview, setCsvPreview] = useState<{
    validCount: number
    errors: ImportRowResult[]
  } | null>(null)

  const csvRowCount = useMemo(() => parseCSV(csvRaw).length, [csvRaw])

  const fileInputRef = useRef<HTMLInputElement>(null)

  function handleFileUpload(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (event) => {
      const text = event.target?.result as string
      setCsvRaw(text)
      setCsvPreview(null)
    }
    reader.readAsText(file)
    e.target.value = "" // reset so the same file can be selected again
  }

  function resetCsv() {
    setCsvRaw("")
    setCsvRows([])
    setCsvPreview(null)
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
    if (!csvPreview) return
    const lines = [
      "email,role,reason",
      ...csvPreview.errors.map((e) => `${e.email},,${e.reason}`),
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
    const parsed = parseCSV(csvRaw)
    if (!parsed.length) return
    setCsvRows(parsed)
    importMembers(
      { rows: parsed, dry_run: true },
      {
        onSuccess: (res) => {
          if (!res.data) return
          setCsvPreview({
            validCount: res.data.valid_count ?? 0,
            errors: res.data.errors ?? [],
          })
        },
      }
    )
  }

  function commitImport() {
    if (!csvPreview) return
    const errorIndexes = new Set(csvPreview.errors.map((e) => e.index))
    const validRows = csvRows.filter((_, i) => !errorIndexes.has(i))
    importMembers(
      { rows: validRows, dry_run: false },
      {
        onSuccess: (res) => {
          toast.success(
            t("organization.members.importResult", {
              count: res.data?.imported ?? 0,
            })
          )
          resetCsv()
          onOpenChange(false)
        },
      }
    )
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          setEmailsRaw("")
          resetCsv()
        }
        onOpenChange(v)
      }}
    >
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            {t("organization.members.addMembers")}
            {seatsLeft !== null && (
              <Badge
                variant={atLimit ? "destructive" : "secondary"}
                className="font-normal"
              >
                {t("organization.invitations.seatsLeft", { count: seatsLeft })}
              </Badge>
            )}
          </DialogTitle>
          <DialogDescription>
            {t("organization.invitations.inviteDescription")}
          </DialogDescription>
        </DialogHeader>

        <Tabs defaultValue="email" className="mt-2">
          <TabsList className="w-full justify-start rounded-xl">
            <TabsTrigger value="email">
              {t("organization.invitations.byEmail")}
            </TabsTrigger>
            <TabsTrigger value="csv">
              {t("organization.members.importTitle")}
            </TabsTrigger>
            {isOwner && (
              <TabsTrigger value="code">
                {t("organization.invitations.byCode")}
              </TabsTrigger>
            )}
          </TabsList>

          <TabsContent
            value="email"
            className="mt-4 flex min-h-[280px] flex-col focus-visible:outline-hidden"
          >
            <form
              onSubmit={(e) => {
                e.preventDefault()
                void form.handleSubmit()
              }}
              className="flex flex-1 flex-col"
            >
              <div className="mb-4 flex flex-col gap-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="invite-emails">
                    {t("organization.invitations.emailsLabel")}
                  </Label>
                  <Textarea
                    id="invite-emails"
                    placeholder="sara@beta.io, tom@acme.com"
                    value={emailsRaw}
                    onChange={(e) => setEmailsRaw(e.target.value)}
                    rows={4}
                  />
                  <p className="mt-1 text-[13px] text-muted-foreground">
                    {t("organization.invitations.emailsDescription")}
                  </p>
                  {invalid.length > 0 && (
                    <p className="text-xs text-destructive">
                      {t("organization.invitations.invalidEmails", {
                        emails: invalid.join(", "),
                      })}
                    </p>
                  )}
                </div>

                <form.Field name="role">
                  {(field) => (
                    <div className="flex flex-col gap-2">
                      <Label>{t("organization.members.roleCol")}</Label>
                      <div className="flex gap-4">
                        {(["member", "admin"] as const).map((r) => (
                          <label
                            key={r}
                            className="flex items-center gap-1.5 text-sm"
                          >
                            <input
                              type="radio"
                              name="role"
                              checked={field.state.value === r}
                              onChange={() => field.handleChange(r)}
                            />
                            {t(`organization.roles.${r}`)}
                          </label>
                        ))}
                      </div>
                      <p className="text-[13px] text-muted-foreground">
                        {t("organization.invitations.roleAppliedToAll")}
                      </p>
                    </div>
                  )}
                </form.Field>
              </div>

              <DialogFooter className="mt-auto border-t pt-4">
                <Button
                  type="submit"
                  disabled={invitePending || valid.length === 0}
                  className="ml-auto"
                  nativeButton={!atLimit}
                  render={
                    atLimit ? (
                      <a href={`/organization/${organizationId}/billing`} />
                    ) : undefined
                  }
                >
                  {invitePending && (
                    <IconLoader2
                      data-icon="inline-start"
                      className="animate-spin"
                    />
                  )}
                  {atLimit
                    ? t("organization.invitations.upgradePlan")
                    : t("organization.invitations.sendInviteCount", {
                        count: valid.length,
                      })}
                </Button>
              </DialogFooter>
            </form>
          </TabsContent>

          <TabsContent
            value="csv"
            className="mt-4 flex min-h-[280px] flex-col gap-4 focus-visible:outline-hidden"
          >
            <div className="relative">
              <Textarea
                placeholder={t("organization.members.importPlaceholder")}
                value={csvRaw}
                onChange={(e) => {
                  setCsvRaw(e.target.value)
                  setCsvPreview(null)
                }}
                rows={8}
                className="pt-11 font-mono text-xs"
              />
              <div className="absolute top-2 right-2">
                <input
                  type="file"
                  accept=".csv"
                  className="hidden"
                  ref={fileInputRef}
                  onChange={handleFileUpload}
                />
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="h-7 text-xs"
                  onClick={() => fileInputRef.current?.click()}
                >
                  <IconUpload className="size-3" data-icon="inline-start" />
                  {t("organization.members.uploadCsv")}
                </Button>
              </div>
            </div>
            <p className="text-[13px] text-muted-foreground">
              {t("organization.members.importCsvDescription")}
            </p>

            {csvPreview && (
              <div className="flex flex-col gap-2 rounded-lg border p-3 text-sm">
                <div className="flex items-center gap-2">
                  <Badge className="border-emerald-500/30 bg-emerald-500/10 text-emerald-600">
                    {t("organization.members.importValidCount", {
                      count: csvPreview.validCount,
                    })}
                  </Badge>
                  {csvPreview.errors.length > 0 && (
                    <Badge className="border-destructive/30 bg-destructive/10 text-destructive">
                      {t("organization.members.importErrorCount", {
                        count: csvPreview.errors.length,
                      })}
                    </Badge>
                  )}
                </div>
                {csvPreview.errors.length > 0 && (
                  <div className="flex flex-col gap-0.5 text-xs text-destructive">
                    {csvPreview.errors.slice(0, 5).map((e) => (
                      <span key={e.index}>
                        {t("organization.members.importRowError", {
                          row: e.index + 1,
                          reason: e.reason,
                        })}
                      </span>
                    ))}
                    {csvPreview.errors.length > 5 && (
                      <span className="text-muted-foreground">
                        {t("organization.members.importMoreErrors", {
                          count: csvPreview.errors.length - 5,
                        })}
                      </span>
                    )}
                  </div>
                )}
                {csvPreview.errors.length > 0 && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={downloadErrorRows}
                    className="mt-1 self-start"
                  >
                    {t("organization.members.downloadErrorRows")}
                  </Button>
                )}
              </div>
            )}

            <DialogFooter className="mt-auto w-full items-center border-t pt-4 sm:justify-between">
              <Button
                variant="link"
                size="sm"
                onClick={downloadTemplate}
                className="px-0 text-muted-foreground"
              >
                <IconDownload data-icon="inline-start" />
                {t("organization.members.downloadTemplate")}
              </Button>
              {csvPreview ? (
                <Button
                  onClick={commitImport}
                  disabled={importPending || csvPreview.validCount === 0}
                >
                  {importPending && (
                    <IconLoader2
                      data-icon="inline-start"
                      className="animate-spin"
                    />
                  )}
                  {t("organization.invitations.sendInviteCount", {
                    count: csvPreview.validCount,
                  })}
                </Button>
              ) : (
                <Button
                  onClick={runDryRun}
                  disabled={importPending || csvRowCount === 0}
                >
                  {importPending && (
                    <IconLoader2
                      data-icon="inline-start"
                      className="animate-spin"
                    />
                  )}
                  {t("organization.members.importPreview", {
                    count: csvRowCount,
                  })}
                </Button>
              )}
            </DialogFooter>
          </TabsContent>

          {isOwner && (
            <TabsContent
              value="code"
              className="mt-4 min-h-[280px] focus-visible:outline-hidden"
            >
              <InviteCodeSection
                organizationId={organizationId}
                isOwner={isOwner}
                bare
              />
            </TabsContent>
          )}
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}
