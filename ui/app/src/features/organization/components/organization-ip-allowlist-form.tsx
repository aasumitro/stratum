import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconX } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogAction,
} from "@/components/ui/alert-dialog"
import { useOrganization } from "@/features/organization/hooks/use-organization"
import { useUpdateOrganizationSettings } from "@/features/organization/hooks/use-settings"

interface Props {
  organizationId: string
  isOwner: boolean
}

function validateCIDR(entry: string): boolean {
  const ipv4 = /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/
  const ipv6 = /^[0-9a-fA-F:]+\/\d{1,3}$/
  return ipv4.test(entry) || ipv6.test(entry)
}

// CIDR chips with the lock-yourself-out guard.
// The backend is the only place that can actually know the caller's real
// client IP (deliberately no third-party IP-detection call added here),
// so the guard surfaces as a blocking dialog on the 422 the save returns,
// rather than a pre-submit "your IP is X" inline warning — a disclosed
// simplification, not a missing safety property.
export function OrganizationIPAllowlistForm({
  organizationId,
  isOwner,
}: Props) {
  const { t } = useTranslation()
  const { data, isLoading } = useOrganization(organizationId)
  const { mutate: updateSettings, isPending } =
    useUpdateOrganizationSettings(organizationId)

  const organization = data?.data
  const persisted = organization?.settings?.allowed_ips ?? []

  const [input, setInput] = useState("")
  const [inputError, setInputError] = useState<string | null>(null)
  const [lockoutDialogOpen, setLockoutDialogOpen] = useState(false)

  const isInputEmpty = input.trim() === ""
  const isInputValid = isInputEmpty || validateCIDR(input.trim())
  const isAddDisabled = isPending || isInputEmpty || !isInputValid

  function handleAdd(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    const value = input.trim()
    if (!value) return
    if (!validateCIDR(value)) {
      setInputError(
        t("organization.settings.allowedIpsInvalid", { entry: value })
      )
      return
    }

    updateSettings(
      {
        timezone: organization?.timezone ?? "",
        locale: organization?.locale ?? "",
        allowed_ips: [...persisted, value],
      },
      {
        onSuccess: () => {
          setInput("")
          setInputError(null)
        },
        onError: (err) => {
          const code = (err as { status?: { code?: string } })?.status?.code
          if (code === "IP_ALLOWLIST_LOCKS_OUT_CALLER") {
            setLockoutDialogOpen(true)
          }
        },
      }
    )
  }

  function removeEntry(entry: string) {
    updateSettings(
      {
        timezone: organization?.timezone ?? "",
        locale: organization?.locale ?? "",
        allowed_ips: persisted.filter((e) => e !== entry),
      },
      {
        onError: (err) => {
          const code = (err as { status?: { code?: string } })?.status?.code
          if (code === "IP_ALLOWLIST_LOCKS_OUT_CALLER") {
            setLockoutDialogOpen(true)
          }
        },
      }
    )
  }

  if (isLoading) {
    return (
      <Card>
        <CardContent className="flex flex-col gap-4 pt-6">
          <Skeleton className="h-24 w-full" />
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("organization.settings.allowedIpsTitle")}</CardTitle>
        <CardDescription>
          {t("organization.settings.allowedIpsDescription")}
        </CardDescription>
      </CardHeader>
      <form onSubmit={handleAdd}>
        <CardContent className="flex flex-col gap-3">
          <div className="flex flex-wrap gap-2">
            {persisted.map((entry) => (
              <span
                key={entry}
                className="inline-flex items-center gap-1 rounded-full bg-muted px-2.5 py-1 font-mono text-xs"
              >
                {entry}
                {isOwner && (
                  <button
                    type="button"
                    disabled={isPending}
                    onClick={() => removeEntry(entry)}
                    aria-label={t("organization.settings.removeIp", { entry })}
                    className="disabled:opacity-50"
                  >
                    <IconX className="size-3" />
                  </button>
                )}
              </span>
            ))}
            {persisted.length === 0 && (
              <span className="text-xs text-muted-foreground">
                {t("organization.settings.allowedIpsEmpty")}
              </span>
            )}
          </div>
          {isOwner && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="ws-allowed-ip-input">
                {t("organization.settings.allowedIpsLabel")}
              </Label>
              <div className="flex gap-2">
                <Input
                  id="ws-allowed-ip-input"
                  placeholder="10.0.0.0/8"
                  value={input}
                  onChange={(e) => {
                    setInput(e.target.value)
                    setInputError(null)
                  }}
                  className="font-mono text-sm"
                />
                <Button
                  type="submit"
                  variant="outline"
                  disabled={isAddDisabled}
                >
                  {isPending ? (
                    <IconLoader2 className="size-4 animate-spin" />
                  ) : (
                    t("common.add")
                  )}
                </Button>
              </div>
              {inputError ? (
                <p className="text-xs text-destructive">{inputError}</p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  {t("organization.settings.allowedIpsHint")}
                </p>
              )}
            </div>
          )}
        </CardContent>
      </form>

      <AlertDialog open={lockoutDialogOpen} onOpenChange={setLockoutDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("organization.settings.lockoutDialogTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("organization.settings.lockoutDialogDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogAction onClick={() => setLockoutDialogOpen(false)}>
              {t("common.dismiss")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
