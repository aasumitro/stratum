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
import { capture } from "@/lib/analytics"
import type {
  Subscription,
  Invoice,
  Payment,
  PaymentLink,
  SubscriptionHistory,
  UsageMetric,
  Entitlement,
  AttachedAddon,
  Coupon,
  InvoicePreview,
} from "@/types/billing"
import type { Plan, Feature, Addon } from "@/types/reference"

// ── Queries ────────────────────────────────────────────────────────────────

export function useBillingSubscription(organizationId: string) {
  return useHTTPQuery<Subscription>({
    queryKey: queryKeys.billing.subscription(organizationId),
    url: API.billing(organizationId),
    options: { retry: false },
  })
}

export function useInvoices(organizationId: string, enabled = true) {
  return useHTTPQuery<Invoice[]>({
    queryKey: queryKeys.billing.invoices(organizationId),
    url: API.billing(organizationId, "invoices"),
    options: { retry: false, enabled },
  })
}

export function usePayments(organizationId: string) {
  return useHTTPQuery<Payment[]>({
    queryKey: queryKeys.billing.payments(organizationId),
    url: API.billing(organizationId, "payments"),
    options: { retry: false },
  })
}

export function useBillingHistory(organizationId: string) {
  return useHTTPQuery<SubscriptionHistory[]>({
    queryKey: queryKeys.billing.history(organizationId),
    url: API.billing(organizationId, "history"),
    options: { retry: false },
  })
}

export function useUsage(organizationId: string) {
  return useHTTPQuery<UsageMetric[]>({
    queryKey: queryKeys.billing.usage(organizationId),
    url: API.billing(organizationId, "usage"),
    options: { retry: false },
  })
}

// countryCode, when passed, scopes the response to that country's currency
// only (server-side — see reference.scopedPrices) instead of every currency
// the catalog stores. Omitting it keeps today's full-currency-map behavior,
// so existing callers (billing settings' plan/addon pickers, which already
// know the org's real currency from its subscription record) are unaffected.
export function usePlans(countryCode?: string) {
  return useHTTPQuery<Plan[]>({
    queryKey: countryCode
      ? [...queryKeys.references.plans(), countryCode]
      : queryKeys.references.plans(),
    url: countryCode
      ? `${API.references("plans")}?country_code=${countryCode}`
      : API.references("plans"),
  })
}

export function useFeatures() {
  return useHTTPQuery<Feature[]>({
    queryKey: queryKeys.references.features(),
    url: API.references("features"),
  })
}

export function useAddonsCatalog(countryCode?: string) {
  return useHTTPQuery<Addon[]>({
    queryKey: countryCode
      ? [...queryKeys.references.addons(), countryCode]
      : queryKeys.references.addons(),
    url: countryCode
      ? `${API.references("addons")}?country_code=${countryCode}`
      : API.references("addons"),
  })
}

export function useAttachedAddons(organizationId: string) {
  return useHTTPQuery<AttachedAddon[]>({
    queryKey: queryKeys.billing.addons(organizationId),
    url: API.billing(organizationId, "addons"),
    options: { retry: false },
  })
}

// useInvoicePreview — omit plan/cycle for the pending-changes bar's
// current-state preview; pass both for the change-plan dialog's
// hypothetical proration preview.
export function useInvoicePreview(
  organizationId: string,
  plan?: string,
  cycle?: string,
  enabled = true
) {
  const params = new URLSearchParams()
  if (plan) params.set("plan", plan)
  if (cycle) params.set("cycle", cycle)
  const qs = params.toString()
  return useHTTPQuery<InvoicePreview>({
    queryKey: [
      ...queryKeys.billing.subscription(organizationId),
      "preview",
      plan,
      cycle,
    ],
    url: `${API.billing(organizationId, "preview")}${qs ? `?${qs}` : ""}`,
    options: { retry: false, enabled },
  })
}

export function useEligibleCoupons(organizationId: string, enabled = true) {
  return useHTTPQuery<Coupon[]>({
    queryKey: [...queryKeys.billing.subscription(organizationId), "coupons"],
    url: API.billing(organizationId, "coupons"),
    options: { retry: false, enabled },
  })
}

// Same eligibility check as useEligibleCoupons, before an organization (and
// its subscription) exist — backs the create-organization cart.
export function useEligibleCouponsForNewOrg(enabled = true) {
  return useHTTPQuery<Coupon[]>({
    queryKey: queryKeys.billing.eligibleCouponsForNewOrg(),
    url: API.eligibleCouponsForNewOrg(),
    options: { retry: false, enabled },
  })
}

// ── Mutations ──────────────────────────────────────────────────────────────

export function useCancelSubscription(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void>({
    url: API.billing(organizationId, "cancel"),
    options: {
      onSuccess: () => {
        toast.success(t("billing.subscription.cancelled"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
      },
      onError: () => toast.error(t("billing.subscription.cancelFailed")),
    },
  })
}

export function useResumeSubscription(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void>({
    url: API.billing(organizationId, "resume"),
    options: {
      onSuccess: () => {
        toast.success(t("billing.subscription.resumed"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
      },
      onError: () => toast.error(t("billing.subscription.resumeFailed")),
    },
  })
}

export function useExtendSubscription(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Invoice, { months: number }>({
    url: API.billing(organizationId, "extend"),
    options: {
      onSuccess: () => {
        toast.success(t("billing.subscription.extended"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.invoices(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.history(organizationId),
        })
      },
      onError: () => toast.error(t("billing.subscription.extendFailed")),
    },
  })
}

export function useActivateTrialNow(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Invoice>({
    url: API.billing(organizationId, "activate"),
    options: {
      onSuccess: () => {
        toast.success(t("billing.trial.activated"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.invoices(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.history(organizationId),
        })
      },
      onError: () => toast.error(t("billing.trial.activateFailed")),
    },
  })
}

export function useChangePlan(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<void, { plan: string; cycle: string }>({
    url: API.billing(organizationId, "plan"),
    options: {
      onSuccess: (_data, vars) => {
        toast.success(t("billing.subscription.planChanged"))
        capture("plan_changed", {
          organization_id: organizationId,
          plan: vars.plan,
          cycle: vars.cycle,
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.invoices(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.paymentLinks(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.history(organizationId),
        })
      },
      onError: () => toast.error(t("billing.subscription.planChangeFailed")),
    },
  })
}

export function usePaymentLinks(organizationId: string) {
  return useHTTPQuery<PaymentLink[]>({
    queryKey: queryKeys.billing.paymentLinks(organizationId),
    url: API.billing(organizationId, "payment-links"),
    options: { retry: false },
  })
}

export function useBillingFeatures(organizationId: string) {
  return useHTTPQuery<Entitlement[]>({
    queryKey: [...queryKeys.billing.subscription(organizationId), "features"],
    url: API.billing(organizationId, "features"),
    options: { retry: false },
  })
}

export function useAttachAddon(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, { addon_id: string; quantity: number }>({
    url: API.billing(organizationId, "addons"),
    options: {
      onSuccess: () => {
        toast.success(t("billing.addons.attached"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.addons(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: [
            ...queryKeys.billing.subscription(organizationId),
            "features",
          ],
        })
      },
      onError: () => toast.error(t("billing.addons.attachFailed")),
    },
  })
}

export function useDetachAddon(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (addonId) => API.billing(organizationId, "addons", addonId),
    options: {
      onSuccess: () => {
        toast.success(t("billing.addons.detached"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.addons(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
        void queryClient.invalidateQueries({
          queryKey: [
            ...queryKeys.billing.subscription(organizationId),
            "features",
          ],
        })
      },
      onError: () => toast.error(t("billing.addons.detachFailed")),
    },
  })
}

export function useRedeemCoupon(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<void, { code: string }>({
    url: API.billing(organizationId, "coupons", "redeem"),
    options: {
      onSuccess: () => {
        toast.success(t("billing.coupons.redeemed"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.billing.subscription(organizationId),
        })
      },
      onError: () => toast.error(t("billing.coupons.invalidCode")),
    },
  })
}
