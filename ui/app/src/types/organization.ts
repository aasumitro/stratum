export type OrganizationStatus = "active" | "suspended" | "deleted"
export type OrganizationRole = "owner" | "admin" | "member"

export interface Organization {
  id: string
  slug: string
  name: string
  status: OrganizationStatus
  owner_id: string
  invite_code?: string
  invite_code_enabled: boolean
  timezone: string
  locale: string
  country_code: string
  settings?: OrganizationSettings
  suspended_at?: string
  suspended_reason?: string
  created_at: string
  updated_at: string
}

export interface OrganizationSettings {
  allowed_ips?: string[]
  logo_url?: string
}

export interface OrganizationFile {
  id: string
  organization_id: string
  folder_id?: string
  name: string
  path: string
  size_bytes: number
  mime_type?: string
  created_by: string
  deleted_at?: string
  created_at: string
}

// Folder — the API returns a flat list; the frontend assembles the tree
// client-side from parent_folder_id.
export interface Folder {
  id: string
  organization_id: string
  parent_folder_id?: string
  name: string
  created_by: string
  created_at: string
  updated_at: string
}

export interface OrganizationView {
  id: string
  slug: string
  name: string
  status: OrganizationStatus
  owner_id: string
  role: OrganizationRole
  joined_at: string
  created_at: string
  updated_at: string
}

export interface Member {
  id: string
  organization_id: string
  auth_sub: string
  role: OrganizationRole
  joined_at: string
  email?: string
  full_name?: string
  avatar_url?: string
}

export interface Invitation {
  id: string
  organization_id: string
  email: string
  role: OrganizationRole
  invited_by: string
  status: string
  expires_at: string
  created_at: string
}

/** GET /me/invitations — the caller's own pending invitations, across every
 * organization. Unlike Invitation (admin-facing), this carries `token`
 * since it's already scoped to the authenticated caller's own email — the
 * same audience the token was already emailed to — to support a true
 * one-click accept from the onboarding screen. */
export interface MyInvitation {
  id: string
  organization_id: string
  organization_name: string
  email: string
  role: OrganizationRole
  invited_by: string
  invited_by_email?: string
  token: string
  expires_at: string
  created_at: string
}

/** GET /invitations/preview — read-only invitation details shown before
 * the caller commits to accepting. */
export interface InvitationPreview {
  organization_id: string
  organization_name: string
  role: OrganizationRole
  invited_by_email?: string
}

/** GET /organizations/join/preview — read-only organization + owner details
 * shown before the caller commits to joining by invite code. */
export interface InviteCodePreview {
  organization_id: string
  organization_name: string
  owner_name?: string
  owner_email?: string
  owner_avatar_url?: string
}

export interface AuditEvent {
  id: string
  organization_id: string
  actor: string
  action: string
  resource: string
  status_code: number
  metadata?: unknown
  ip: string
  user_agent: string
  created_at: string
}

export interface WebhookHealth {
  success_percent_24h: number
  delivered_24h: number
  total_24h: number
}

export interface WebhookEndpoint {
  id: string
  url: string
  enabled: boolean
  secret?: string
  subscribed_events?: string[]
  auto_disabled_at?: string
  secret_rotation_expires_at?: string
  health?: WebhookHealth
  created_at: string
  updated_at: string
}

export interface WebhookDeliveryAttempt {
  attempt: number
  attempted_at: string
  status_code?: number
  latency_ms?: number
  error?: string
}

export interface WebhookDelivery {
  id: string
  endpoint_id: string
  event_id: string
  event_type: string
  status: string
  attempts: number
  last_error?: string
  response_status_code?: number
  response_body?: string
  latency_ms?: number
  attempts_log?: WebhookDeliveryAttempt[]
  delivered_at?: string
  created_at: string
}

export type PermissionFeature =
  | "members"
  | "invitations"
  | "files"
  | "webhooks"
  | "auditLog"
  | "billing"
  | "settingsGeneral"
  | "settingsSecurity"
  | "settingsDanger"
export type PermissionAction = "view" | "manage" | "edit" | "delete"
