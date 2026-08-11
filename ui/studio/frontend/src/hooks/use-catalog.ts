import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import type {
  AddonFeatureInput,
  AddonInput,
  CouponInput,
  FeatureInput,
  PlanFeatureInput,
  PlanInput,
} from "../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { CatalogService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

// --- Plans ---

export function usePlans(projectId: string) {
  return useQuery({
    queryKey: ["catalog", "plans", projectId],
    queryFn: () => CatalogService.ListPlans(projectId),
    staleTime: 60_000,
    retry: 1,
  })
}

export function useCreatePlan(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: PlanInput) =>
      CatalogService.CreatePlan(projectId, input),
    onSuccess: () => {
      toast.success("Plan created")
      qc.invalidateQueries({ queryKey: ["catalog", "plans", projectId] })
    },
    onError: (err) => toast.error(`Create failed: ${wailsError(err)}`),
  })
}

export function useUpdatePlan(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ planID, input }: { planID: string; input: PlanInput }) =>
      CatalogService.UpdatePlan(projectId, planID, input),
    onSuccess: () => {
      toast.success("Plan updated")
      qc.invalidateQueries({ queryKey: ["catalog", "plans", projectId] })
    },
    onError: (err) => toast.error(`Update failed: ${wailsError(err)}`),
  })
}

export function useDeletePlan(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (planID: string) =>
      CatalogService.DeletePlan(projectId, planID),
    onSuccess: () => {
      toast.success("Plan deleted")
      qc.invalidateQueries({ queryKey: ["catalog", "plans", projectId] })
    },
    onError: (err) => toast.error(`Delete failed: ${wailsError(err)}`),
  })
}

// --- Features ---

export function useFeatures(projectId: string) {
  return useQuery({
    queryKey: ["catalog", "features", projectId],
    queryFn: () => CatalogService.ListFeatures(projectId),
    staleTime: 60_000,
    retry: 1,
  })
}

export function useCreateFeature(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: FeatureInput) =>
      CatalogService.CreateFeature(projectId, input),
    onSuccess: () => {
      toast.success("Feature created")
      qc.invalidateQueries({ queryKey: ["catalog", "features", projectId] })
    },
    onError: (err) => toast.error(`Create failed: ${wailsError(err)}`),
  })
}

export function useUpdateFeature(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      featureID,
      input,
    }: {
      featureID: string
      input: FeatureInput
    }) => CatalogService.UpdateFeature(projectId, featureID, input),
    onSuccess: () => {
      toast.success("Feature updated")
      qc.invalidateQueries({ queryKey: ["catalog", "features", projectId] })
    },
    onError: (err) => toast.error(`Update failed: ${wailsError(err)}`),
  })
}

export function useDeleteFeature(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (featureID: string) =>
      CatalogService.DeleteFeature(projectId, featureID),
    onSuccess: () => {
      toast.success("Feature deleted")
      qc.invalidateQueries({ queryKey: ["catalog", "features", projectId] })
    },
    onError: (err) => toast.error(`Delete failed: ${wailsError(err)}`),
  })
}

// --- Plan Features (entitlement management) ---

export function usePlanFeatures(projectId: string, planId: string) {
  return useQuery({
    queryKey: ["catalog", "plan-features", projectId, planId],
    queryFn: () => CatalogService.ListPlanFeatures(projectId, planId),
    enabled: !!planId,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useUpsertPlanFeature(projectId: string, planId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: PlanFeatureInput) =>
      CatalogService.UpsertPlanFeature(projectId, planId, input),
    onSuccess: () => {
      toast.success("Entitlement saved")
      qc.invalidateQueries({
        queryKey: ["catalog", "plan-features", projectId, planId],
      })
    },
    onError: (err) => toast.error(`Save failed: ${wailsError(err)}`),
  })
}

export function useDeletePlanFeature(projectId: string, planId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (featureID: string) =>
      CatalogService.DeletePlanFeature(projectId, planId, featureID),
    onSuccess: () => {
      toast.success("Entitlement removed")
      qc.invalidateQueries({
        queryKey: ["catalog", "plan-features", projectId, planId],
      })
    },
    onError: (err) => toast.error(`Remove failed: ${wailsError(err)}`),
  })
}

// --- Coupons ---

export function useCoupons(projectId: string) {
  return useQuery({
    queryKey: ["catalog", "coupons", projectId],
    queryFn: () => CatalogService.ListCoupons(projectId),
    staleTime: 30_000,
    retry: 1,
  })
}

export function useCreateCoupon(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: CouponInput) =>
      CatalogService.CreateCoupon(projectId, input),
    onSuccess: () => {
      toast.success("Coupon created")
      qc.invalidateQueries({ queryKey: ["catalog", "coupons", projectId] })
    },
    onError: (err) => toast.error(`Create failed: ${wailsError(err)}`),
  })
}

export function useUpdateCoupon(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ code, input }: { code: string; input: CouponInput }) =>
      CatalogService.UpdateCoupon(projectId, code, input),
    onSuccess: () => {
      toast.success("Coupon updated")
      qc.invalidateQueries({ queryKey: ["catalog", "coupons", projectId] })
    },
    onError: (err) => toast.error(`Update failed: ${wailsError(err)}`),
  })
}

export function useDeleteCoupon(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => CatalogService.DeleteCoupon(projectId, code),
    onSuccess: () => {
      toast.success("Coupon deleted")
      qc.invalidateQueries({ queryKey: ["catalog", "coupons", projectId] })
    },
    onError: (err) => toast.error(`Delete failed: ${wailsError(err)}`),
  })
}

// --- Coupon Targets ---

export function useCouponTargets(projectId: string, couponCode: string) {
  return useQuery({
    queryKey: ["catalog", "coupon-targets", projectId, couponCode],
    queryFn: () => CatalogService.ListCouponTargets(projectId, couponCode),
    enabled: !!couponCode,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useAddCouponTarget(projectId: string, couponCode: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      subjectType,
      subjectId,
    }: {
      subjectType: string
      subjectId: string
    }) =>
      CatalogService.AddCouponTarget(
        projectId,
        couponCode,
        subjectType,
        subjectId
      ),
    onSuccess: () => {
      toast.success("Target added")
      qc.invalidateQueries({
        queryKey: ["catalog", "coupon-targets", projectId, couponCode],
      })
    },
    onError: (err) => toast.error(`Add failed: ${wailsError(err)}`),
  })
}

export function useRemoveCouponTarget(projectId: string, couponCode: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      subjectType,
      subjectId,
    }: {
      subjectType: string
      subjectId: string
    }) =>
      CatalogService.RemoveCouponTarget(
        projectId,
        couponCode,
        subjectType,
        subjectId
      ),
    onSuccess: () => {
      toast.success("Target removed")
      qc.invalidateQueries({
        queryKey: ["catalog", "coupon-targets", projectId, couponCode],
      })
    },
    onError: (err) => toast.error(`Remove failed: ${wailsError(err)}`),
  })
}

// --- Addons ---

export function useAddons(projectId: string) {
  return useQuery({
    queryKey: ["catalog", "addons", projectId],
    queryFn: () => CatalogService.ListAddons(projectId),
    staleTime: 60_000,
    retry: 1,
  })
}

export function useCreateAddon(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: AddonInput) =>
      CatalogService.CreateAddon(projectId, input),
    onSuccess: () => {
      toast.success("Addon created")
      qc.invalidateQueries({ queryKey: ["catalog", "addons", projectId] })
    },
    onError: (err) => toast.error(`Create failed: ${wailsError(err)}`),
  })
}

export function useUpdateAddon(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ addonID, input }: { addonID: string; input: AddonInput }) =>
      CatalogService.UpdateAddon(projectId, addonID, input),
    onSuccess: () => {
      toast.success("Addon updated")
      qc.invalidateQueries({ queryKey: ["catalog", "addons", projectId] })
    },
    onError: (err) => toast.error(`Update failed: ${wailsError(err)}`),
  })
}

export function useDeleteAddon(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (addonID: string) =>
      CatalogService.DeleteAddon(projectId, addonID),
    onSuccess: () => {
      toast.success("Addon deleted")
      qc.invalidateQueries({ queryKey: ["catalog", "addons", projectId] })
    },
    onError: (err) => toast.error(`Delete failed: ${wailsError(err)}`),
  })
}

// --- Addon Features (entitlement management) ---

export function useAddonFeatures(projectId: string, addonId: string) {
  return useQuery({
    queryKey: ["catalog", "addon-features", projectId, addonId],
    queryFn: () => CatalogService.ListAddonFeatures(projectId, addonId),
    enabled: !!addonId,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useUpsertAddonFeature(projectId: string, addonId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: AddonFeatureInput) =>
      CatalogService.UpsertAddonFeature(projectId, addonId, input),
    onSuccess: () => {
      toast.success("Entitlement saved")
      qc.invalidateQueries({
        queryKey: ["catalog", "addon-features", projectId, addonId],
      })
    },
    onError: (err) => toast.error(`Save failed: ${wailsError(err)}`),
  })
}

export function useDeleteAddonFeature(projectId: string, addonId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (featureID: string) =>
      CatalogService.DeleteAddonFeature(projectId, addonId, featureID),
    onSuccess: () => {
      toast.success("Entitlement removed")
      qc.invalidateQueries({
        queryKey: ["catalog", "addon-features", projectId, addonId],
      })
    },
    onError: (err) => toast.error(`Remove failed: ${wailsError(err)}`),
  })
}
