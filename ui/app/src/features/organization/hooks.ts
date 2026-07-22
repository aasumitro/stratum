import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { useHTTPQuery } from "@/lib/api/query"
import {
  useHTTPActionPost,
  useHTTPActionPatch,
  useHTTPActionDelete,
  useHTTPActionUpload,
} from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { api } from "@/lib/api/axios"
import type { HTTPResponse } from "@/lib/api/response"
import { capture } from "@/lib/analytics"
import type {
  Organization,
  OrganizationView,
  Member,
  Invitation,
  MyInvitation,
  InvitationPreview,
  InviteCodePreview,
  OrganizationRole,
  AuditEvent,
  WebhookEndpoint,
  WebhookDelivery,
  OrganizationFile,
  Folder,
} from "@/types/organization"
import type { Country } from "@/types/reference"

// ── Queries ────────────────────────────────────────────────────────────────

export function useCountries() {
  return useHTTPQuery<Country[]>({
    queryKey: queryKeys.references.countries(),
    url: API.references("countries"),
  })
}

export function useOrganizations() {
  return useHTTPQuery<OrganizationView[]>({
    queryKey: queryKeys.organizations.list(),
    url: API.organizations(),
  })
}

export function useOrganizationMembers(
  organizationId: string,
  options?: { enabled?: boolean }
) {
  return useHTTPQuery<Member[]>({
    queryKey: queryKeys.organizations.members(organizationId),
    url: API.organizations(organizationId, "members"),
    options,
  })
}

export function useOrganization(
  organizationId: string,
  options?: { retry?: boolean }
) {
  return useHTTPQuery<Organization>({
    queryKey: queryKeys.organizations.detail(organizationId),
    url: API.organizations(organizationId),
    options,
  })
}

export function useOrganizationInvitations(
  organizationId: string,
  enabled = true
) {
  return useHTTPQuery<Invitation[]>({
    queryKey: queryKeys.organizations.invitations(organizationId),
    url: API.organizations(organizationId, "invitations"),
    options: { enabled },
  })
}

/** Chip filters — every field optional, all sent as query params to
 * both the list and export routes so export always matches what's on screen. */
export interface AuditLogFilter {
  actor?: string
  action?: string
  resource?: string
  from?: string
  to?: string
}

function auditLogParams(filter: AuditLogFilter, extra: Record<string, string>) {
  const params = new URLSearchParams(extra)
  if (filter.actor) params.set("actor", filter.actor)
  if (filter.action) params.set("action", filter.action)
  if (filter.resource) params.set("resource", filter.resource)
  if (filter.from) params.set("from", filter.from)
  if (filter.to) params.set("to", filter.to)
  return params
}

export function useAuditLog(
  organizationId: string,
  filter: AuditLogFilter = {},
  cursor?: string,
  limit = 20,
  enabled = true
) {
  const params = auditLogParams(filter, { limit: String(limit) })
  if (cursor !== undefined) params.set("cursor", cursor)
  return useHTTPQuery<AuditEvent[]>({
    queryKey: [
      ...queryKeys.organizations.auditLog(organizationId),
      filter,
      cursor,
      limit,
    ],
    url: `${API.organizations(organizationId, "audit-log")}?${params.toString()}`,
    options: { enabled },
  })
}

export function auditLogExportUrl(
  organizationId: string,
  filter: AuditLogFilter
) {
  const qs = auditLogParams(filter, {}).toString()
  return `${API.organizations(organizationId, "audit-log", "export")}${qs ? `?${qs}` : ""}`
}

export function useAcceptInvitation() {
  return useHTTPActionPost<unknown, { token: string }>({
    url: API.invitations("accept"),
  })
}

export function useDeclineInvitation() {
  return useHTTPActionPost<unknown, { token: string }>({
    url: API.invitations("decline"),
  })
}

/** Read-only invitation details, validated the same way accept is, but
 * without committing — backs the "Join {org}?" confirm card. */
export function useInvitationPreview(token: string) {
  return useHTTPQuery<InvitationPreview>({
    queryKey: ["invitations", "preview", token],
    url: `${API.invitations("preview")}?token=${encodeURIComponent(token)}`,
    options: { enabled: !!token, retry: false },
  })
}

/** The caller's own pending invitations across every organization — backs
 * the onboarding auto-surface. */
export function useMyInvitations(enabled = true) {
  return useHTTPQuery<MyInvitation[]>({
    queryKey: queryKeys.account.myInvitations(),
    url: API.me("invitations"),
    options: { enabled, retry: false },
  })
}

/** Notifies the ORIGINAL inviter (not the caller) that a lost/expired
 * invitation needs resending. */
export function useRequestNewInvitation() {
  return useHTTPActionPost<unknown, { token: string }>({
    url: API.invitations("request-new"),
  })
}

// ── Mutations ──────────────────────────────────────────────────────────────

export function useUpdateOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<Organization, { name: string; slug: string }>({
    url: API.organizations(organizationId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.settings.updated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.settings.updateFailed")),
    },
  })
}

export function useUpdateOrganizationSettings(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    Organization,
    { timezone: string; locale: string; allowed_ips?: string[] }
  >({
    url: API.organizations(organizationId, "settings"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.settings.settingsUpdated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
      },
      onError: () =>
        toast.error(t("organization.settings.settingsUpdateFailed")),
    },
  })
}

export function useChangeMemberRole(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    Member,
    { authSub: string; role: OrganizationRole }
  >({
    url: ({ authSub }) =>
      API.organizations(organizationId, "members", authSub, "role"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.roleUpdated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
      },
      onError: () => toast.error(t("organization.members.roleUpdateFailed")),
    },
  })
}

export function useRemoveMember(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (authSub) => API.organizations(organizationId, "members", authSub),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.removed"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
      },
      onError: () => toast.error(t("organization.members.removeFailed")),
    },
  })
}

export function useLeaveOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void>({
    url: API.organizations(organizationId, "leave"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.leftOrganization"))
        if (localStorage.getItem("active_organization_id") === organizationId) {
          localStorage.removeItem("active_organization_id")
          void api.patch(API.me("preferences"), {
            default_organization_id: null,
          })
        }
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.members.leaveFailed")),
    },
  })
}

export function useRevokeInvitation(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (invId) => API.organizations(organizationId, "invitations", invId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.invitations.revoked"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
      onError: () => toast.error(t("organization.invitations.revokeFailed")),
    },
  })
}

export function useInvite(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<
    Invitation,
    { email: string; role: OrganizationRole }
  >({
    url: API.organizations(organizationId, "invitations"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.invitations.sent"))
        capture("member_invited", {
          organization_id: organizationId,
          via: "email",
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
      onError: (err) =>
        toast.error(
          err?.status?.message ?? t("organization.invitations.sendFailed")
        ),
    },
  })
}

export function useResendInvitation(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Invitation, { email: string; role: string }>({
    url: API.organizations(organizationId, "invitations"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.invitations.resent"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
      onError: () => toast.error(t("organization.invitations.resendFailed")),
    },
  })
}

export function useDeleteOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void>({
    url: API.organizations(organizationId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.danger.deleted"))
        if (localStorage.getItem("active_organization_id") === organizationId) {
          localStorage.removeItem("active_organization_id")
          void api.patch(API.me("preferences"), {
            default_organization_id: null,
          })
        }
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.danger.deleteFailed")),
    },
  })
}

export function useGenerateInviteCode(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Organization>({
    url: API.organizations(organizationId, "invite-code"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.inviteCode.generated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
      },
      onError: () => toast.error(t("organization.inviteCode.generateFailed")),
    },
  })
}

export function useToggleInviteCode(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<Organization, { enabled: boolean }>({
    url: API.organizations(organizationId, "invite-code"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        }),
      onError: () => toast.error(t("organization.inviteCode.updateFailed")),
    },
  })
}

export function useTransferOwnership(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, { auth_sub: string }>({
    url: API.organizations(organizationId, "transfer"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.members.transferred"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.members(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.members.transferFailed")),
    },
  })
}

// Danger zone — self-service pause/resume, distinct from the
// billing-driven or Studio-operator-driven suspend paths.
export function useSuspendOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, { reason?: string }>({
    url: API.organizations(organizationId, "suspend"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.danger.suspended"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: () => toast.error(t("organization.danger.suspendFailed")),
    },
  })
}

export function useUnsuspendOrganization(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, void>({
    url: API.organizations(organizationId, "unsuspend"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.danger.unsuspended"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        })
      },
      onError: (err) => {
        const code = (err as { status?: { code?: string } })?.status?.code
        toast.error(
          code === "BILLING_HOLD"
            ? t("organization.danger.unsuspendBillingHold")
            : t("organization.danger.unsuspendFailed")
        )
      },
    },
  })
}

/** Read-only invite-code details, validated the same way join is, but
 * without committing — backs the onboarding "Join with an invite code"
 * card's check-then-confirm flow. */
export function useInviteCodePreview(code: string) {
  return useHTTPQuery<InviteCodePreview>({
    queryKey: ["organizations", "join", "preview", code],
    url: `${API.organizations("join", "preview")}?code=${encodeURIComponent(code)}`,
    options: { enabled: code.trim().length === 8, retry: false },
  })
}

export function useJoinOrganization() {
  const queryClient = useQueryClient()
  return useHTTPActionPost<OrganizationView, { code: string }>({
    url: API.organizations("join"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.list(),
        }),
    },
  })
}

export function useWebhooks(organizationId: string, enabled = true) {
  return useHTTPQuery<WebhookEndpoint[]>({
    queryKey: queryKeys.organizations.webhooks(organizationId),
    url: API.organizations(organizationId, "webhooks"),
    options: { enabled },
  })
}

export interface WebhookDeliveryFilter {
  status?: string
  event_type?: string
}

export function useWebhookDeliveries(
  organizationId: string,
  webhookId: string,
  filter: WebhookDeliveryFilter = {},
  cursor?: string,
  enabled = true
) {
  const params = new URLSearchParams()
  if (filter.status) params.set("status", filter.status)
  if (filter.event_type) params.set("event_type", filter.event_type)
  if (cursor) params.set("cursor", cursor)
  const qs = params.toString()
  return useHTTPQuery<WebhookDelivery[]>({
    queryKey: [
      ...queryKeys.organizations.webhookDeliveries(organizationId, webhookId),
      filter,
      cursor,
    ],
    url:
      API.organizations(organizationId, "webhooks", webhookId, "deliveries") +
      (qs ? `?${qs}` : ""),
    options: { enabled },
  })
}

export function useCreateWebhook(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<
    WebhookEndpoint & { secret: string },
    { url: string; subscribed_events?: string[] }
  >({
    url: API.organizations(organizationId, "webhooks"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.webhooks.created"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhooks(organizationId),
        })
      },
    },
  })
}

export function useUpdateWebhook(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    WebhookEndpoint,
    { id: string; url: string; enabled: boolean; subscribed_events?: string[] }
  >({
    url: ({ id }) => API.organizations(organizationId, "webhooks", id),
    options: {
      onSuccess: () => {
        toast.success(t("organization.webhooks.updated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhooks(organizationId),
        })
      },
    },
  })
}

export function useDeleteWebhook(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (webhookId) =>
      API.organizations(organizationId, "webhooks", webhookId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.webhooks.deleted"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhooks(organizationId),
        })
      },
    },
  })
}

export function useRotateWebhookSecret(organizationId: string) {
  const queryClient = useQueryClient()
  return useHTTPActionPost<
    { id: string; secret: string; secret_rotation_expires_at?: string },
    string
  >({
    url: (webhookId) =>
      API.organizations(organizationId, "webhooks", webhookId, "rotate-secret"),
    options: {
      onSuccess: () =>
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhooks(organizationId),
        }),
    },
  })
}

export function useSendWebhookTestEvent(organizationId: string) {
  const queryClient = useQueryClient()
  return useHTTPActionPost<
    { success: boolean; delivery: WebhookDelivery },
    string
  >({
    url: (webhookId) =>
      API.organizations(organizationId, "webhooks", webhookId, "test-event"),
    options: {
      onSuccess: (_res, webhookId) => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhooks(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhookDeliveries(
            organizationId,
            webhookId
          ),
        })
      },
    },
  })
}

export function useRetryDelivery(organizationId: string, webhookId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, string>({
    url: (deliveryId) =>
      API.organizations(
        organizationId,
        "webhooks",
        webhookId,
        "deliveries",
        deliveryId,
        "retry"
      ),
    options: {
      onSuccess: () => {
        toast.success(t("organization.webhooks.retried"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhookDeliveries(
            organizationId,
            webhookId
          ),
        })
      },
    },
  })
}

export function useRetryAllFailedDeliveries(
  organizationId: string,
  webhookId: string
) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<{ retried: number }, void>({
    url: API.organizations(
      organizationId,
      "webhooks",
      webhookId,
      "deliveries",
      "retry-failed"
    ),
    options: {
      onSuccess: (res) => {
        toast.success(
          t("organization.webhooks.retriedAll", {
            count: res.data?.retried ?? 0,
          })
        )
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.webhookDeliveries(
            organizationId,
            webhookId
          ),
        })
      },
    },
  })
}

export interface ImportRow {
  email: string
  role: OrganizationRole
}
export interface ImportRowResult {
  index: number
  email: string
  reason: string
}

/** Bulk-invites by email with a dry-run preview — pass `dry_run:
 * true` to validate without committing, `false` to actually create the
 * invitations. Both modes return the same per-row error shape. */
export function useImportMembers(organizationId: string) {
  const queryClient = useQueryClient()
  return useHTTPActionPost<
    {
      valid_count?: number
      imported?: number
      errors?: ImportRowResult[]
      failed?: ImportRowResult[]
    },
    { rows: ImportRow[]; dry_run: boolean }
  >({
    url: API.organizations(organizationId, "members", "import"),
    options: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.invitations(organizationId),
        })
      },
    },
  })
}

// ── Logo ───────────────────────────────────────────────────────────────────

export function useUploadLogo(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionUpload<void>({
    url: API.organizations(organizationId, "logo"),
    fieldName: "logo",
    options: {
      onSuccess: () => {
        toast.success(t("organization.logoUploaded"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.detail(organizationId),
        })
      },
    },
  })
}

// ── Files ──────────────────────────────────────────────────────────────────

// folderId scopes the listing to one folder (undefined = root);
// search + searchAll implement the "search this folder" vs "search all
// folders" toggle.
export function useOrganizationFiles(
  organizationId: string,
  folderId?: string,
  search?: string,
  searchAll?: boolean,
  cursor?: string,
  enabled = true
) {
  const params = new URLSearchParams({ limit: "20" })
  if (folderId) params.set("folder_id", folderId)
  if (search) params.set("search", search)
  if (searchAll) params.set("search_all", "true")
  if (cursor) params.set("cursor", cursor)
  return useHTTPQuery<{ items: OrganizationFile[]; next_cursor: string }>({
    queryKey: queryKeys.organizations.files(
      organizationId,
      folderId,
      search,
      searchAll,
      cursor
    ),
    url: `${API.files(organizationId)}?${params}`,
    options: { enabled },
  })
}

export function useOrganizationTrash(organizationId: string, enabled = true) {
  return useHTTPQuery<OrganizationFile[]>({
    queryKey: queryKeys.organizations.trash(organizationId),
    url: API.files(organizationId, "trash"),
    options: { enabled },
  })
}

function invalidateFileQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  organizationId: string
) {
  void queryClient.invalidateQueries({
    queryKey: ["organizations", organizationId, "files"],
  })
}

// useUploadFile is deliberately NOT the shared useHTTPActionUpload wrapper
// — per-file upload progress + cancel needs a raw axios
// call with onUploadProgress and an AbortController the shared upload
// hook's fire-and-forget FormData post doesn't expose. Callers track
// progress/cancel state themselves (see files-page.tsx's upload queue).
export async function uploadOrganizationFile(
  organizationId: string,
  file: File,
  folderId: string | undefined,
  onProgress: (percent: number) => void,
  signal: AbortSignal
): Promise<OrganizationFile> {
  const form = new FormData()
  form.append("file", file)
  if (folderId) form.append("folder_id", folderId)
  const res = await api.post<HTTPResponse<OrganizationFile>>(
    API.files(organizationId),
    form,
    {
      signal,
      onUploadProgress: (e) => {
        if (e.total) onProgress(Math.round((e.loaded / e.total) * 100))
      },
    }
  )
  return res.data.data!
}

export function useDeleteOrganizationFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (fileId) => API.files(organizationId, fileId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.deletedToTrash"))
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function useBulkDeleteFiles(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<{ deleted_count: number }, { file_ids: string[] }>({
    url: `${API.files(organizationId, "bulk-delete")}`,
    options: {
      onSuccess: (res) => {
        toast.success(
          t("organization.files.bulkDeleted", {
            count: res.data?.deleted_count ?? 0,
          })
        )
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function useMoveFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    OrganizationFile,
    { fileId: string; folder_id: string | null }
  >({
    url: ({ fileId }) => API.files(organizationId, fileId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.moved"))
        invalidateFileQueries(queryClient, organizationId)
      },
      onError: () => toast.error(t("organization.files.moveFailed")),
    },
  })
}

export function useRestoreFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<OrganizationFile, string>({
    url: (fileId) => API.files(organizationId, fileId, "restore"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.restored"))
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function usePurgeFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (fileId) => API.files(organizationId, fileId, "permanent"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.purged"))
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

// ── Folders ────────────────────────────────────────────────────────────────

export function useOrganizationFolders(organizationId: string) {
  return useHTTPQuery<Folder[]>({
    queryKey: queryKeys.organizations.folders(organizationId),
    url: API.files(organizationId, "folders"),
  })
}

export function useCreateFolder(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Folder, { name: string; parent_folder_id?: string }>(
    {
      url: API.files(organizationId, "folders"),
      options: {
        onSuccess: () => {
          toast.success(t("organization.files.folderCreated"))
          void queryClient.invalidateQueries({
            queryKey: queryKeys.organizations.folders(organizationId),
          })
        },
        onError: () => toast.error(t("organization.files.folderCreateFailed")),
      },
    }
  )
}

export function useUpdateFolder(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    Folder,
    { folderId: string; name?: string; parent_folder_id?: string | null }
  >({
    url: ({ folderId }) => API.files(organizationId, "folders", folderId),
    options: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.folders(organizationId),
        })
      },
      onError: () => toast.error(t("organization.files.folderUpdateFailed")),
    },
  })
}

export function useDeleteFolder(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (folderId) => API.files(organizationId, "folders", folderId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.folderDeleted"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.folders(organizationId),
        })
      },
      onError: (err) => {
        const code = (err as { status?: { code?: string } })?.status?.code
        toast.error(
          code === "FOLDER_NOT_EMPTY"
            ? t("organization.files.folderNotEmpty")
            : t("organization.files.folderDeleteFailed")
        )
      },
    },
  })
}
