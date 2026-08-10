import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { useHTTPQuery } from "@/lib/api/query"
import {
  useHTTPActionPost,
  useHTTPActionPatch,
  useHTTPActionDelete,
} from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { useBillingFeatures } from "@/features/billing/hooks"
import type { WebhookEndpoint, WebhookDelivery } from "@/types/organization"

export function useWebhooks(organizationId: string, enabled = true) {
  return useHTTPQuery<WebhookEndpoint[]>({
    queryKey: queryKeys.organizations.webhooks(organizationId),
    url: API.organizations(organizationId, "webhooks"),
    options: { enabled },
  })
}

const WEBHOOKS_FEATURE_ID = "webhooks"

export type WebhooksContentState = "loading" | "locked" | "empty" | "available"

/**
 * Resolves which of Webhooks' three content states applies for this org —
 * shared by the Settings preview and the full webhooks panel so both agree
 * on the same state at the same moment. Precedence: loading beats locked
 * beats empty beats available.
 */
export function useWebhooksContentState(
  organizationId: string,
  canAccessWebhooks: boolean
) {
  const { data: featuresData, isLoading: featuresLoading } =
    useBillingFeatures(organizationId)
  const { data: webhooksData, isLoading: webhooksLoading } = useWebhooks(
    organizationId,
    canAccessWebhooks
  )

  const hasWebhooksFeature = (featuresData?.data ?? []).some(
    (f) => f.feature_id === WEBHOOKS_FEATURE_ID
  )
  const endpoints = webhooksData?.data ?? []
  const isLoading = featuresLoading || (canAccessWebhooks && webhooksLoading)

  const state: WebhooksContentState = isLoading
    ? "loading"
    : !hasWebhooksFeature
      ? "locked"
      : endpoints.length === 0
        ? "empty"
        : "available"

  return { state, hasWebhooksFeature, endpoints }
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
        const deliveriesKey = queryKeys.organizations.webhookDeliveries(
          organizationId,
          webhookId
        )
        void queryClient.invalidateQueries({ queryKey: deliveriesKey })
        // Test events are dispatched asynchronously. Refetch again shortly after
        // to ensure the newly created delivery appears in the UI.
        setTimeout(() => {
          void queryClient.invalidateQueries({ queryKey: deliveriesKey })
        }, 1000)
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
        const deliveriesKey = queryKeys.organizations.webhookDeliveries(
          organizationId,
          webhookId
        )
        void queryClient.invalidateQueries({ queryKey: deliveriesKey })
        // Retries are processed asynchronously. Refetch again shortly after.
        setTimeout(() => {
          void queryClient.invalidateQueries({ queryKey: deliveriesKey })
        }, 1000)
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
        const deliveriesKey = queryKeys.organizations.webhookDeliveries(
          organizationId,
          webhookId
        )
        void queryClient.invalidateQueries({ queryKey: deliveriesKey })
        // Bulk retries are processed asynchronously. Refetch again shortly after.
        setTimeout(() => {
          void queryClient.invalidateQueries({ queryKey: deliveriesKey })
        }, 1000)
      },
    },
  })
}
