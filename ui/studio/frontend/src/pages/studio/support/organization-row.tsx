import { useState } from "react"
import {
  IconAlertTriangle,
  IconChevronDown,
  IconChevronRight,
} from "@tabler/icons-react"
import type { UserOrganization } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { useActivateSubscription, useExtendTrial } from "@/hooks/use-support"
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
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { TableCell, TableRow } from "@/components/ui/table"
import { billingBadge, formatDate, roleBadge } from "./utils"
import { InvoiceSection } from "./invoice-section"
import { ChangePlanSheet } from "./change-plan-sheet"

interface OrganizationRowProps {
  projectId: string
  authSub: string
  organization: UserOrganization
}

export function OrganizationRow({
  projectId,
  authSub,
  organization,
}: OrganizationRowProps) {
  const [expanded, setExpanded] = useState(false)
  const [trialDays, setTrialDays] = useState(7)
  const [showExtend, setShowExtend] = useState(false)
  const [showActivate, setShowActivate] = useState(false)
  const [showChangePlan, setShowChangePlan] = useState(false)

  const extendTrial = useExtendTrial(projectId, authSub)
  const activate = useActivateSubscription(projectId, authSub)

  return (
    <>
      <TableRow
        className="cursor-pointer hover:bg-muted/50"
        onClick={() => setExpanded((v) => !v)}
      >
        <TableCell className="py-2">
          <div className="flex items-center gap-1.5">
            {expanded ? (
              <IconChevronDown className="size-3.5 text-muted-foreground" />
            ) : (
              <IconChevronRight className="size-3.5 text-muted-foreground" />
            )}
            <span className="text-sm font-medium">{organization.name}</span>
            <span className="font-mono text-xs text-muted-foreground">
              /{organization.slug}
            </span>
          </div>
        </TableCell>
        <TableCell className="py-2">{roleBadge(organization.role)}</TableCell>
        <TableCell className="py-2 text-xs">
          {organization.plan_name || organization.plan || "—"}
        </TableCell>
        <TableCell className="py-2">
          {billingBadge(organization.billing_status)}
        </TableCell>
        <TableCell className="py-2 text-xs text-muted-foreground">
          {organization.trial_end
            ? formatDate(organization.trial_end)
            : organization.period_end
              ? formatDate(organization.period_end)
              : "—"}
        </TableCell>
        <TableCell
          className="py-2 text-right"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="flex items-center justify-end gap-1">
            {organization.billing_status === "trialing" && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 px-2 text-xs"
                onClick={() => setShowExtend(true)}
              >
                Extend trial
              </Button>
            )}
            {(organization.billing_status === "trialing" ||
              organization.billing_status === "cancelled" ||
              organization.billing_status === "past_due") && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 px-2 text-xs"
                onClick={() => setShowActivate(true)}
              >
                Activate
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-xs"
              onClick={() => setShowChangePlan(true)}
            >
              Change plan
            </Button>
          </div>
        </TableCell>
      </TableRow>

      {expanded && (
        <TableRow>
          <TableCell colSpan={6} className="bg-muted/20 p-0">
            <InvoiceSection projectId={projectId} organization={organization} />
          </TableCell>
        </TableRow>
      )}

      {/* Extend trial dialog */}
      <AlertDialog
        open={showExtend}
        onOpenChange={(o) => {
          if (!o) setShowExtend(false)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Extend trial — {organization.name}
            </AlertDialogTitle>
            <AlertDialogDescription>
              Add days to the current trial end date.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="px-6 py-2">
            <Label htmlFor="trial-days">Additional days</Label>
            <Input
              id="trial-days"
              type="number"
              min={1}
              max={365}
              value={trialDays}
              onChange={(e) => setTrialDays(Number(e.target.value))}
              className="mt-1.5 w-32"
            />
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setShowExtend(false)}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                extendTrial.mutate({
                  organizationID: organization.id,
                  days: trialDays,
                })
                setShowExtend(false)
              }}
            >
              Extend by {trialDays} day{trialDays !== 1 ? "s" : ""}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Activate dialog */}
      <AlertDialog
        open={showActivate}
        onOpenChange={(o) => {
          if (!o) setShowActivate(false)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <IconAlertTriangle className="size-5 text-amber-500" />
              Activate subscription — {organization.name}
            </AlertDialogTitle>
            <AlertDialogDescription>
              This will set the subscription status to active and clear the
              trial end date. No invoice is generated.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setShowActivate(false)}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                activate.mutate(organization.id)
                setShowActivate(false)
              }}
            >
              Activate now
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {showChangePlan && (
        <ChangePlanSheet
          projectId={projectId}
          authSub={authSub}
          organization={organization}
          onClose={() => setShowChangePlan(false)}
        />
      )}
    </>
  )
}
