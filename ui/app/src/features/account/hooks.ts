import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useHTTPQuery } from "@/lib/api/query"
import {
  useHTTPActionPatch,
  useHTTPActionPost,
  useHTTPActionDelete,
  useHTTPActionUpload,
} from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { supabase } from "@/lib/auth/supabase"
import type { UserProfile, UserTask, LoginEvent } from "@/types/account"
import type { AuditEvent } from "@/types/organization"

// ── Queries ────────────────────────────────────────────────────────────────

export function useProfile() {
  return useHTTPQuery<UserProfile>({
    queryKey: queryKeys.account.me(),
    url: API.me(),
  })
}

// Personal audit trail — every mutating action this user took, across every
// organization, not just one. Distinct from useAuditLog in features/organization,
// which is scoped to a single organization's admin view. from/to are RFC3339
// timestamps, same bounds the CSV export already accepts.
export function useAuditLog(
  cursor?: string,
  limit = 20,
  from?: string,
  to?: string
) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (cursor !== undefined) params.set("cursor", cursor)
  if (from) params.set("from", from)
  if (to) params.set("to", to)
  return useHTTPQuery<AuditEvent[]>({
    queryKey: [...queryKeys.account.auditLog(), cursor, limit, from, to],
    url: `${API.me("audit-log")}?${params.toString()}`,
  })
}

// ── Mutations ──────────────────────────────────────────────────────────────

export function useUpdateProfile() {
  const queryClient = useQueryClient()
  return useHTTPActionPatch<
    UserProfile,
    Partial<Pick<UserProfile, "full_name" | "avatar_url">>
  >({
    url: API.me(),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({ queryKey: queryKeys.account.me() }),
    },
  })
}

export function useUpdatePreferences() {
  const queryClient = useQueryClient()
  return useHTTPActionPatch<UserProfile, Record<string, unknown>>({
    url: API.me("preferences"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({ queryKey: queryKeys.account.me() }),
    },
  })
}

export function useDeleteAccount() {
  return useHTTPActionDelete<UserTask>({ url: API.me() })
}

export function useExportData() {
  return useHTTPActionPost<UserTask>({ url: API.me("export") })
}

export function useTasks() {
  return useHTTPQuery<UserTask[]>({
    queryKey: queryKeys.account.tasks(),
    url: API.me("tasks"),
  })
}

export function usePollTask(taskId: string | null) {
  return useHTTPQuery<UserTask>({
    queryKey: queryKeys.account.task(taskId ?? ""),
    url: API.me("tasks", taskId ?? ""),
    options: {
      enabled: !!taskId,
      refetchInterval: (query) => {
        const status = query.state.data?.data?.status
        return status === "completed" || status === "failed" ? false : 2000
      },
    },
  })
}

export function useSessions(cursor?: string) {
  const params = new URLSearchParams({ limit: "50" })
  if (cursor) params.set("cursor", cursor)
  return useHTTPQuery<LoginEvent[]>({
    queryKey: queryKeys.account.sessions(cursor),
    url: `${API.me("sessions")}?${params.toString()}`,
  })
}

export function useRevokeAllSessions() {
  return useHTTPActionPost<void>({ url: API.me("sessions", "revoke-all") })
}

export function useUploadAvatar() {
  const queryClient = useQueryClient()
  return useHTTPActionUpload<{ avatar_url: string }>({
    url: API.me("avatar"),
    fieldName: "avatar",
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({ queryKey: queryKeys.account.me() }),
    },
  })
}

export function useDeleteAvatar() {
  const queryClient = useQueryClient()
  return useHTTPActionDelete<void>({
    url: API.me("avatar"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({ queryKey: queryKeys.account.me() }),
    },
  })
}

// ── MFA ────────────────────────────────────────────────────────────────────
// Enrollment/verification talks to Supabase Auth directly (auth.mfa.*) —
// not our backend. `useSyncMfaStatus` is the one call that hits our API, so
// the backend can authoritatively record whether MFA is enabled (see
// internal/modules/account syncMFAStatus — never trusts a client-supplied
// value).

export function useMfaFactors() {
  return useQuery({
    queryKey: queryKeys.account.mfaFactors(),
    queryFn: async () => {
      const { data, error } = await supabase.auth.mfa.listFactors()
      if (error) throw error
      return data.all
    },
  })
}

export function useEnrollTotpFactor() {
  return useMutation({
    mutationFn: async () => {
      const { data, error } = await supabase.auth.mfa.enroll({
        factorType: "totp",
      })
      if (error) throw error
      return data
    },
  })
}

export function useVerifyTotpFactor() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (vars: { factorId: string; code: string }) => {
      const { data, error } = await supabase.auth.mfa.challengeAndVerify(vars)
      if (error) throw error
      return data
    },
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.account.mfaFactors(),
      }),
  })
}

export function useUnenrollMfaFactor() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (factorId: string) => {
      const { error } = await supabase.auth.mfa.unenroll({ factorId })
      if (error) throw error
    },
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.account.mfaFactors(),
      }),
  })
}

export function useSyncMfaStatus() {
  return useHTTPActionPost<{ mfa_enabled: boolean }>({
    url: API.me("mfa", "sync"),
  })
}

// Records a password change for the audit trail — call with no arguments,
// right after supabase.auth.updateUser({ password }) succeeds. Never pass a
// body here; the backend rejects anything but an empty request.
export function useRecordPasswordChanged() {
  return useHTTPActionPost<void>({
    url: API.me("password", "changed"),
  })
}
