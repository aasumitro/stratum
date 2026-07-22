export type TaskStatus = "pending" | "processing" | "completed" | "failed"

export interface LoginEvent {
  id: string
  auth_sub: string
  ip_address: string
  user_agent: string
  created_at: string
}

export interface UserProfile {
  id: string
  auth_sub: string
  email: string
  full_name: string
  avatar_url: string
  preferences: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface UserTask {
  id: string
  kind: string
  status: TaskStatus
  result?: unknown
  created_at: string
  updated_at: string
}
