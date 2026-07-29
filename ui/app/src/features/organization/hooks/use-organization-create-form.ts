import { useEffect, useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useSelector } from "@tanstack/react-store"
import { useTranslation } from "react-i18next"
import { useHTTPActionPost } from "@/lib/api/action"
import { API } from "@/lib/api/path"
import { parseApiError } from "@/lib/api/error"
import { useCountries, useOrganizations } from "./use-organization"
import { usePlans } from "@/features/billing/hooks"
import type { Organization } from "@/types/organization"

/**
 * Shared form logic for creating an organization — used by
 * create-organization-form.tsx, the Details -> Plan -> Review wizard
 * rendered both in onboarding and in the post-onboarding "create another
 * organization" dialog. Centralizing here so both surfaces stay in sync.
 *
 * Every organization creation goes through Details -> Plan -> Review,
 * first organization or not — plan and cycle are required by the API (no
 * server-side default), so the form always sends an explicit choice.
 * isFirstOrganization only changes what the Plan/Review copy says (7-day
 * trial vs immediate billing) — see billing.service_subscription's
 * count == 0 trial-eligibility check.
 */
export function useOrganizationCreateForm(
  onSuccess: (org: Organization) => void
) {
  const { t } = useTranslation()
  const [serverError, setServerError] = useState<string | null>(null)

  const { data: countriesData } = useCountries()
  const countries = countriesData?.data ?? []

  const { data: organizationsData } = useOrganizations()
  const isFirstOrganization = (organizationsData?.data ?? []).length === 0

  const { mutate, isPending } = useHTTPActionPost<
    Organization,
    {
      name: string
      slug: string
      country_code: string
      plan: string
      cycle: string
      addons?: { addon_id: string; quantity: number }[]
      coupon_code?: string
    }
  >({
    url: API.organizations(),
    options: {
      onSuccess: (res) => {
        if (res.data) onSuccess(res.data)
      },
      onError: (err) =>
        setServerError(parseApiError(err, t("onboarding.organization.failed"))),
    },
  })

  const form = useForm({
    defaultValues: {
      name: "",
      slug: "",
      country_code: "",
      plan: "solo",
      cycle: "monthly",
      // addon_id -> quantity, and an optional coupon code — both sent in
      // the same create request (see contracts.CatalogReader.ValidateCouponCode
      // and organization.createOrganization's up-front cart validation) so
      // they're already priced into a 2nd+ organization's first invoice,
      // rather than raced against async subscription provisioning via
      // separate post-creation calls.
      addons: {} as Record<string, number>,
      coupon_code: "",
    },
    onSubmit: ({ value }) => {
      setServerError(null)
      const addons = Object.entries(value.addons).map(
        ([addon_id, quantity]) => ({
          addon_id,
          quantity,
        })
      )
      mutate({
        name: value.name.trim(),
        slug: value.slug.trim(),
        country_code: value.country_code,
        plan: value.plan,
        cycle: value.cycle,
        ...(addons.length > 0 && { addons }),
        ...(value.coupon_code.trim() && {
          coupon_code: value.coupon_code.trim(),
        }),
      })
    },
  })

  // Reactive read (not a plain form.state.values.country_code access,
  // which wouldn't re-render this hook's own component when the field
  // changes) — the plan list must refetch scoped to whichever country the
  // user has picked so far in Details.
  const countryCode = useSelector(form.store, (s) => s.values.country_code)
  const { data: plansData, isLoading: plansLoading } = usePlans(countryCode)
  // "custom" is sales-assisted (see PlanSelector's contact-us treatment on
  // the billing page) — not a self-serve choice at org-creation time.
  const plans = (plansData?.data ?? [])
    .filter((p) => p.active && p.id !== "custom")
    .sort((a, b) => a.sort_order - b.sort_order)

  // Default to the first active country once the list loads, rather than
  // hardcoding a country that may not even be active in this deployment
  // (a fresh deploy only has Indonesia active by default).
  // Only applies while the field is still untouched, so it never
  // overwrites a country the user already picked.
  useEffect(() => {
    if (
      countries.length > 0 &&
      !form.state.values.country_code &&
      !form.state.fieldMeta.country_code?.isTouched
    ) {
      form.setFieldValue("country_code", countries[0].code)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [countries.length])

  return {
    form,
    countries,
    plans,
    plansLoading,
    isFirstOrganization,
    isPending,
    serverError,
  }
}

export type OrganizationCreateForm = ReturnType<
  typeof useOrganizationCreateForm
>
