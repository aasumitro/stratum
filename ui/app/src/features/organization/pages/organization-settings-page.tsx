import { useState } from "react"
import { useParams, useNavigate, useSearch } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import {
  useOrganization,
  useWebhooksContentState,
} from "@/features/organization/hooks"
import { useBillingFeatures } from "@/features/billing/hooks"
import { usePermissions } from "@/hooks/use-permissions"
import { SettingsAnchorNav } from "@/features/organization/components/settings-anchor-nav"
import { SettingsSection } from "@/features/organization/components/settings-section"
import { DocumentationLinks } from "@/features/organization/components/documentation-links"
import { OrganizationDetailsCard } from "@/features/organization/components/organization-details-card"
import { OrganizationLogoForm } from "@/features/organization/components/organization-logo-form"
import { OrganizationIPAllowlistForm } from "@/features/organization/components/organization-ip-allowlist-form"
import { OrganizationDangerZone } from "@/features/organization/components/organization-danger-zone"
import { MembersTable } from "@/features/organization/components/members-table"
import { WebhooksSectionPreview } from "@/features/organization/components/webhooks-section-preview"
import { WebhooksPanel } from "@/features/organization/components/webhooks-panel"
import { AuditLogSectionPreview } from "@/features/organization/components/audit-log-section-preview"
import { AuditLogTable } from "@/features/organization/components/audit-log-table"
import { AuditLogFilters } from "@/features/organization/components/audit-log-filters"
import { AddMembersDialog } from "@/features/organization/components/add-members-dialog"
import { OverlayPanel } from "@/components/shared/overlay-panel"
import { FeatureGateCard } from "@/components/shared/feature-gate-card"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

// Settings is one continuous page, composed of anchor-linked sections,
// instead of separate routes/tabs: General, Members, Webhooks, Security,
// Audit Log, Danger Zone. Each section is filtered by role before it
// renders at all (ADR-0017: absent, never shown locked) — Webhooks/Audit
// Log/Danger Zone simply aren't in the DOM below their role threshold,
// there's no route left to guard directly.
export function OrganizationSettingsPage() {
  const { t } = useTranslation()
  const { organizationId } = useParams({ strict: false }) as {
    organizationId: string
  }
  const { data: wsData } = useOrganization(organizationId)
  const {
    role,
    isOwner,
    isAdminUp,
    canManageMembers,
    canAccessWebhooks,
    canAccessAuditLog,
    isBillingBlocked,
  } = usePermissions()
  const organization = wsData?.data
  const [addMembersOpen, setAddMembersOpen] = useState(false)
  const [auditLogTotal, setAuditLogTotal] = useState(0)

  const { hasWebhooksFeature } = useWebhooksContentState(
    organizationId,
    canAccessWebhooks
  )

  const { data: featuresData } = useBillingFeatures(organizationId)
  const membersFeature = (featuresData?.data ?? []).find(
    (f) => f.feature_id === "members"
  )
  // -1 is the "unlimited" sentinel (Custom plan) — never treat it as locked.
  // `limit` already folds in purchased add-on seats server-side, so a Solo
  // org that bought +1 Member seats unlocks this the same way a plan
  // upgrade would, with no separate add-on check needed here.
  const isMemberSeatLocked =
    membersFeature?.limit != null &&
    membersFeature.limit !== -1 &&
    membersFeature.limit <= 1

  // Members and Webhooks are billing-blockable (org-state axis, independent
  // of role) — mirrors the sidebar's own amber-dot treatment, now that
  // there's no sidebar row left to carry it for either of them directly.
  const billingDot = isBillingBlocked && (
    <Tooltip>
      <TooltipTrigger
        render={
          <span className="size-1.5 shrink-0 rounded-full bg-amber-500" />
        }
      />
      <TooltipContent>{t("nav.billingLockedTooltip")}</TooltipContent>
    </Tooltip>
  )

  const navigate = useNavigate()
  const search = useSearch({
    from: "/_protected/organization/$organizationId/settings",
  })

  function setPanel(panel: "webhooks" | "audit-log" | undefined) {
    void navigate({
      to: "/organization/$organizationId/settings",
      params: { organizationId },
      search: { ...search, panel, webhookId: undefined },
      resetScroll: false,
    })
  }

  function setWebhookId(webhookId: string | undefined) {
    void navigate({
      to: "/organization/$organizationId/settings",
      params: { organizationId },
      search: { ...search, webhookId },
      resetScroll: false,
    })
  }

  const anchorItems = [
    { id: "general", label: t("organization.settings.sections.general") },
    { id: "members", label: t("organization.settings.sections.members") },
    ...(canAccessWebhooks
      ? [
          {
            id: "webhooks",
            label: t("organization.settings.sections.webhooks"),
          },
        ]
      : []),
    { id: "security", label: t("organization.settings.sections.security") },
    ...(canAccessAuditLog
      ? [
          {
            id: "audit-log",
            label: t("organization.settings.sections.auditLog"),
          },
        ]
      : []),
    ...(isOwner
      ? [{ id: "danger", label: t("organization.settings.sections.danger") }]
      : []),
  ]

  return (
    <div className="flex flex-col">
      <SettingsAnchorNav items={anchorItems} />

      <AddMembersDialog
        organizationId={organizationId}
        open={addMembersOpen}
        onOpenChange={setAddMembersOpen}
        isOwner={isOwner}
      />

      <div className="flex w-full flex-col pt-10 pb-24">
        <SettingsSection
          id="general"
          title={t("organization.settings.sections.general")}
          description={t("organization.settings.generalDescription")}
          docLinks={
            <DocumentationLinks
              labels={[t("organization.settings.docsGeneral2")]}
            />
          }
        >
          <div className="flex flex-col gap-6">
            {isAdminUp && organization && (
              <OrganizationLogoForm organization={organization} />
            )}
            <OrganizationDetailsCard
              organizationId={organizationId}
              isOwner={isOwner}
            />
          </div>
        </SettingsSection>

        <SettingsSection
          id="members"
          title={t("organization.settings.sections.members")}
          titleBadge={billingDot}
          description={t("organization.members.settingsDescription")}
          actions={
            canManageMembers &&
            !isMemberSeatLocked && (
              <Button size="sm" onClick={() => setAddMembersOpen(true)}>
                {t("organization.members.addMembers")}
              </Button>
            )
          }
          docLinks={
            <DocumentationLinks
              labels={[
                t("organization.settings.docsMembers1"),
                t("organization.settings.docsMembers2"),
              ]}
            />
          }
        >
          {isMemberSeatLocked ? (
            <FeatureGateCard
              reason="plan"
              title={t("organization.members.seatGateTitle")}
              description={t("organization.members.seatGateDescription")}
              cta={{
                label: t("organization.webhooks.upgradeCta"),
                onClick: () =>
                  void navigate({
                    to: "/organization/$organizationId/billing",
                    params: { organizationId },
                  }),
              }}
            />
          ) : (
            <MembersTable organizationId={organizationId} currentRole={role} />
          )}
        </SettingsSection>

        {canAccessWebhooks && (
          <SettingsSection
            id="webhooks"
            title={t("organization.settings.sections.webhooks")}
            titleBadge={billingDot}
            description={t("organization.webhooks.settingsDescription")}
            actions={
              hasWebhooksFeature ? (
                <Button size="sm" onClick={() => setPanel("webhooks")}>
                  {t("organization.webhooks.viewAll")}
                </Button>
              ) : undefined
            }
            docLinks={
              <DocumentationLinks
                labels={[
                  t("organization.settings.docsWebhooks1"),
                  t("organization.settings.docsWebhooks2"),
                ]}
              />
            }
          >
            <WebhooksSectionPreview
              organizationId={organizationId}
              canAccessWebhooks={canAccessWebhooks}
              onViewAll={() => setPanel("webhooks")}
            />
          </SettingsSection>
        )}

        <WebhooksPanel
          organizationId={organizationId}
          open={canAccessWebhooks && search.panel === "webhooks"}
          webhookId={search.webhookId}
          onOpenChange={(open) => setPanel(open ? "webhooks" : undefined)}
          onWebhookIdChange={setWebhookId}
        />

        <SettingsSection
          id="security"
          title={t("organization.settings.sections.security")}
          description={t("organization.settings.securityDescription")}
          docLinks={
            <DocumentationLinks
              labels={[t("organization.settings.docsSecurity1")]}
            />
          }
        >
          <OrganizationIPAllowlistForm
            organizationId={organizationId}
            isOwner={isOwner}
          />
        </SettingsSection>

        {canAccessAuditLog && (
          <SettingsSection
            id="audit-log"
            title={t("organization.settings.sections.auditLog")}
            description={t("organization.auditLog.pageDescription")}
            actions={
              <Button size="sm" onClick={() => setPanel("audit-log")}>
                {t("organization.auditLog.viewFullLog")}
              </Button>
            }
            docLinks={
              <DocumentationLinks
                labels={[
                  t("organization.settings.docsAuditLog1"),
                  t("organization.settings.docsAuditLog2"),
                ]}
              />
            }
          >
            <AuditLogSectionPreview organizationId={organizationId} />
          </SettingsSection>
        )}

        <OverlayPanel
          open={canAccessAuditLog && search.panel === "audit-log"}
          onOpenChange={(open) => setPanel(open ? "audit-log" : undefined)}
          title={t("organization.settings.sections.auditLog")}
          headerContent={
            canAccessAuditLog && search.panel === "audit-log" ? (
              <AuditLogFilters
                organizationId={organizationId}
                total={auditLogTotal}
              />
            ) : undefined
          }
          bodyClassName="p-4 md:p-0"
        >
          <AuditLogTable
            organizationId={organizationId}
            onTotalChange={setAuditLogTotal}
          />
        </OverlayPanel>

        {isOwner && organization && (
          <SettingsSection
            id="danger"
            title={t("organization.settings.sections.danger")}
            description={t("organization.danger.description")}
            docLinks={
              <DocumentationLinks
                labels={[
                  t("organization.settings.docsDanger3"),
                  t("organization.settings.docsDanger1"),
                  t("organization.settings.docsDanger2"),
                ]}
              />
            }
          >
            <OrganizationDangerZone
              organizationId={organizationId}
              slug={organization.slug}
              status={organization.status}
            />
          </SettingsSection>
        )}
      </div>
    </div>
  )
}
