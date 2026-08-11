import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { SupportService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export function useSearchUsers(projectId: string, q: string) {
  return useQuery({
    queryKey: ["support", "users", projectId, q],
    queryFn: () => SupportService.SearchUsers(projectId, q),
    enabled: true,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useUserDetail(projectId: string, authSub: string) {
  return useQuery({
    queryKey: ["support", "user", projectId, authSub],
    queryFn: () => SupportService.GetUserDetail(projectId, authSub),
    enabled: !!authSub,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useUserLoginHistory(
  projectId: string,
  authSub: string,
  enabled: boolean
) {
  return useQuery({
    queryKey: ["support", "login-history", projectId, authSub],
    queryFn: () => SupportService.GetUserLoginHistory(projectId, authSub),
    enabled: enabled && !!authSub,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useOrganizationInvoices(
  projectId: string,
  organizationId: string,
  enabled: boolean
) {
  return useQuery({
    queryKey: ["support", "invoices", projectId, organizationId],
    queryFn: () =>
      SupportService.GetOrganizationInvoices(projectId, organizationId),
    enabled: enabled && !!organizationId,
    staleTime: 30_000,
    retry: 1,
  })
}

export function useExtendTrial(projectId: string, authSub: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      organizationID,
      days,
    }: {
      organizationID: string
      days: number
    }) => SupportService.ExtendTrial(projectId, organizationID, days),
    onSuccess: () => {
      toast.success("Trial extended")
      qc.invalidateQueries({
        queryKey: ["support", "user", projectId, authSub],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useActivateSubscription(projectId: string, authSub: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (organizationID: string) =>
      SupportService.ActivateSubscription(projectId, organizationID),
    onSuccess: () => {
      toast.success("Subscription activated")
      qc.invalidateQueries({
        queryKey: ["support", "user", projectId, authSub],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useChangePlan(projectId: string, authSub: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      organizationID,
      plan,
      cycle,
    }: {
      organizationID: string
      plan: string
      cycle: string
    }) => SupportService.ChangePlan(projectId, organizationID, plan, cycle),
    onSuccess: () => {
      toast.success("Plan changed")
      qc.invalidateQueries({
        queryKey: ["support", "user", projectId, authSub],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useMarkInvoicePaid(projectId: string, organizationId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (invoiceID: string) =>
      SupportService.MarkInvoicePaid(projectId, invoiceID),
    onSuccess: () => {
      toast.success("Invoice marked as paid")
      qc.invalidateQueries({
        queryKey: ["support", "invoices", projectId, organizationId],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useVoidInvoice(projectId: string, organizationId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (invoiceID: string) =>
      SupportService.VoidInvoice(projectId, invoiceID),
    onSuccess: () => {
      toast.success("Invoice voided")
      qc.invalidateQueries({
        queryKey: ["support", "invoices", projectId, organizationId],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useAtRiskInvoices(projectId: string) {
  return useQuery({
    queryKey: ["support", "at-risk", projectId],
    queryFn: () => SupportService.GetAtRiskInvoices(projectId),
    staleTime: 30_000,
    retry: false,
    enabled: !!projectId,
  })
}

export function useMarkAtRiskPaid(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (invoiceID: string) =>
      SupportService.MarkInvoicePaid(projectId, invoiceID),
    onSuccess: () => {
      toast.success("Invoice marked as paid")
      qc.invalidateQueries({ queryKey: ["support", "at-risk", projectId] })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useVoidAtRiskInvoice(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (invoiceID: string) =>
      SupportService.VoidInvoice(projectId, invoiceID),
    onSuccess: () => {
      toast.success("Invoice voided")
      qc.invalidateQueries({ queryKey: ["support", "at-risk", projectId] })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}
