import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { InviteCodeSection } from "@/features/organization/components/invite-code-section"
import { useInvite } from "@/features/organization/hooks"
import { useBillingFeatures } from "@/features/billing/hooks"
import type { OrganizationRole } from "@/types/organization"

interface Props {
  organizationId: string
  open: boolean
  onOpenChange: (v: boolean) => void
  isOwner: boolean
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

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

// "By email" / "By code" tabs in a single Invite dialog.
export function InviteEmailSheet({
  organizationId,
  open,
  onOpenChange,
  isOwner,
}: Props) {
  const { t } = useTranslation()
  const { mutate: invite, isPending } = useInvite(organizationId)
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
      // Bulk paste sends one invite per address — invalid ones are simply
      // never submitted, valid ones still send (1k).
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

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{t("organization.invitations.inviteTitle")}</SheetTitle>
          <SheetDescription>
            {t("organization.invitations.inviteDescription")}
          </SheetDescription>
        </SheetHeader>

        <div className="mx-4 mt-6">
          <Tabs defaultValue="email">
            <TabsList>
              <TabsTrigger value="email">
                {t("organization.invitations.byEmail")}
              </TabsTrigger>
              {isOwner && (
                <TabsTrigger value="code">
                  {t("organization.invitations.byCode")}
                </TabsTrigger>
              )}
            </TabsList>

            <TabsContent value="email">
              <form
                onSubmit={(e) => {
                  e.preventDefault()
                  void form.handleSubmit()
                }}
                className="flex flex-col gap-4"
              >
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
                    </div>
                  )}
                </form.Field>

                <div className="flex items-center justify-between">
                  {seatsLeft !== null && (
                    <span className="text-xs text-muted-foreground">
                      {t("organization.invitations.seatsLeft", {
                        count: seatsLeft,
                      })}
                    </span>
                  )}
                  <Button
                    type="submit"
                    disabled={isPending || valid.length === 0}
                    className="ml-auto"
                    nativeButton={!atLimit}
                    render={
                      atLimit ? (
                        <a href={`/organization/${organizationId}/billing`} />
                      ) : undefined
                    }
                  >
                    {isPending && (
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
                </div>
              </form>
            </TabsContent>

            {isOwner && (
              <TabsContent value="code">
                <InviteCodeSection
                  organizationId={organizationId}
                  isOwner={isOwner}
                  bare
                />
              </TabsContent>
            )}
          </Tabs>
        </div>
      </SheetContent>
    </Sheet>
  )
}
