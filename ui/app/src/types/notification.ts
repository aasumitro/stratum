export interface NotificationPreference {
  auth_sub: string
  channel: string
  event_type: string
  enabled: boolean
}

export interface Notification {
  id: string
  organization_id: string
  auth_sub?: string
  kind: string
  channel: string
  subject: string
  body: string
  payload: Record<string, unknown>
  status: string
  sent_at?: string
  read_at?: string
  created_at: string
  updated_at: string
}
