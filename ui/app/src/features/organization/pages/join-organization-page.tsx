import { useEffect, useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { IconLoader2, IconCheck, IconX } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { parseApiError } from "@/lib/api/error"
import { useJoinOrganization } from "@/features/organization/hooks"

type ViewState = "idle" | "success" | "error"

export function JoinOrganizationPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = useSearch({ strict: false }) as { code?: string }
  const urlCode = search.code ?? ""

  const [code, setCode] = useState(urlCode)
  const [viewState, setViewState] = useState<ViewState>("idle")
  const [errorMsg, setErrorMsg] = useState("")
  const [organizationName, setOrganizationName] = useState("")

  const { mutate: joinOrganization, isPending } = useJoinOrganization()

  function join(codeToJoin: string) {
    joinOrganization(
      { code: codeToJoin.trim() },
      {
        onSuccess: (res) => {
          setOrganizationName(res.data?.name ?? "")
          setViewState("success")
        },
        onError: (err) => {
          setErrorMsg(
            parseApiError(err, t("organization.inviteCode.joinFailed"))
          )
          setViewState("error")
        },
      }
    )
  }

  useEffect(() => {
    if (urlCode) join(urlCode)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-6 text-center">
      {isPending && (
        <>
          <IconLoader2 className="size-10 animate-spin text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            {t("organization.inviteCode.joining")}
          </p>
        </>
      )}

      {!isPending && viewState === "success" && (
        <>
          <div className="flex size-16 items-center justify-center rounded-full bg-emerald-500/10">
            <IconCheck className="size-8 text-emerald-500" />
          </div>
          <div>
            <h1 className="text-xl font-extrabold">
              {t("organization.inviteCode.joinedTitle")}
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {t("organization.inviteCode.joinedDesc", {
                name: organizationName,
              })}
            </p>
          </div>
          <Button onClick={() => void navigate({ to: "/organizations" })}>
            {t("organization.invitations.goToOrganizations")}
          </Button>
        </>
      )}

      {!isPending && (viewState === "idle" || viewState === "error") && (
        <div className="flex w-full max-w-sm flex-col gap-6">
          {viewState === "error" && (
            <div className="mx-auto flex size-16 items-center justify-center rounded-full bg-destructive/10">
              <IconX className="size-8 text-destructive" />
            </div>
          )}
          <div>
            <h1 className="text-xl font-extrabold">
              {t("organization.inviteCode.joinTitle")}
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {t("organization.inviteCode.joinDesc")}
            </p>
          </div>
          <div className="flex flex-col gap-1.5 text-left">
            <Label htmlFor="invite-code">
              {t("onboarding.organization.inviteCode")}
            </Label>
            <Input
              id="invite-code"
              placeholder={t("onboarding.organization.inviteCodePlaceholder")}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && code.trim()) {
                  setErrorMsg("")
                  join(code)
                }
              }}
            />
            {errorMsg && <p className="text-xs text-destructive">{errorMsg}</p>}
          </div>
          <Button
            disabled={!code.trim()}
            onClick={() => {
              setErrorMsg("")
              join(code)
            }}
          >
            {t("organization.inviteCode.joinBtn")}
          </Button>
        </div>
      )}
    </div>
  )
}
