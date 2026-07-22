import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useHTTPQuery } from "@/lib/api/query"
import { useHTTPActionPatch } from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { SERVER_URL } from "@/lib/api/axios"
import type { Notification, NotificationPreference } from "@/types/notification"

// ── Queries ────────────────────────────────────────────────────────────────

export function useNotificationCount(organizationId?: string) {
  const url = organizationId
    ? `${API.me("notifications", "unread-count")}?organization_id=${organizationId}`
    : API.me("notifications", "unread-count")
  return useHTTPQuery<{ unread: number }>({
    queryKey: queryKeys.account.notificationCount(organizationId),
    url,
    options: {},
  })
}

export function useNotifications(
  organizationId?: string,
  cursor?: string,
  limit = 20,
  channel?: string
) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (organizationId) params.set("organization_id", organizationId)
  if (cursor !== undefined) params.set("cursor", cursor)
  if (channel) params.set("channel", channel)
  return useHTTPQuery<Notification[]>({
    queryKey: [
      ...queryKeys.account.notifications(organizationId),
      cursor,
      limit,
      channel,
    ],
    url: `${API.me("notifications")}?${params.toString()}`,
    options: { retry: false },
  })
}

// useNotificationStream opens a single SSE connection for the whole session.
// The backend stream is per-user (not per-organization), so no organizationId needed.
// Falls back to 60s polling after 3 connection failures.
// Call this once in ProtectedLayout — never inside page components.
export function useNotificationStream() {
  const queryClient = useQueryClient()

  useEffect(() => {
    let failureCount = 0
    let pollTimer: ReturnType<typeof setInterval> | null = null
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let es: EventSource | null = null

    const invalidate = () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.account.notifications(),
      })
      void queryClient.invalidateQueries({
        queryKey: queryKeys.account.notificationCount(),
      })
    }

    const startPoll = () => {
      pollTimer = setInterval(invalidate, 60_000)
    }

    const connect = () => {
      es = new EventSource(
        `${SERVER_URL}${API.me("notifications", "stream")}`,
        { withCredentials: true }
      )

      es.addEventListener("notification", invalidate)

      es.onerror = () => {
        es?.close()
        es = null
        failureCount++
        if (failureCount >= 3) {
          startPoll()
        } else {
          retryTimer = setTimeout(
            connect,
            Math.min(1_000 * Math.pow(2, failureCount), 30_000)
          )
        }
      }
    }

    connect()

    return () => {
      es?.close()
      if (pollTimer) clearInterval(pollTimer)
      if (retryTimer) clearTimeout(retryTimer)
    }
  }, [queryClient])
}

export function useNotificationPreferences() {
  return useHTTPQuery<NotificationPreference[]>({
    queryKey: queryKeys.account.notificationPreferences(),
    url: API.me("notifications", "preferences"),
    options: { retry: false },
  })
}

// ── Mutations ──────────────────────────────────────────────────────────────

export function useUpdateNotificationPreference() {
  const queryClient = useQueryClient()
  return useHTTPActionPatch<
    void,
    { channel: string; event_type: string; enabled: boolean }
  >({
    url: API.me("notifications", "preferences"),
    options: {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: queryKeys.account.notificationPreferences(),
        }),
    },
  })
}

export function useMarkNotificationRead(organizationId?: string) {
  const queryClient = useQueryClient()
  return useHTTPActionPatch<void, string>({
    url: (id) => API.me("notifications", id, "read"),
    options: {
      onSuccess: () => {
        // invalidate all notification list variants and the count
        void queryClient.invalidateQueries({
          queryKey: queryKeys.account.notifications(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.account.notificationCount(),
        })
      },
    },
  })
}

export function useMarkAllNotificationsRead(organizationId?: string) {
  const queryClient = useQueryClient()
  const url = organizationId
    ? `${API.me("notifications", "read-all")}?organization_id=${organizationId}`
    : API.me("notifications", "read-all")
  return useHTTPActionPatch<void, void>({
    url,
    options: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.account.notifications(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.account.notificationCount(),
        })
      },
    },
  })
}
