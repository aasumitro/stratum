import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { OrganizationOpsService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

const PAGE_SIZE = 50

export function useOrganizations(
  projectId: string,
  status: string,
  offset: number
) {
  return useQuery({
    queryKey: ["organizations", projectId, status, offset],
    queryFn: () =>
      OrganizationOpsService.ListOrganizations(
        projectId,
        status,
        PAGE_SIZE,
        offset
      ),
    staleTime: 30_000,
    retry: 1,
  })
}

export function useOrganizationCount(projectId: string, status: string) {
  return useQuery({
    queryKey: ["organizations", "count", projectId, status],
    queryFn: () => OrganizationOpsService.CountOrganizations(projectId, status),
    staleTime: 30_000,
    retry: 1,
  })
}

export function useSuspendOrganization(projectId: string, status: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      organizationID,
      reason,
    }: {
      organizationID: string
      reason: string
    }) =>
      OrganizationOpsService.SuspendOrganization(
        projectId,
        organizationID,
        reason
      ),
    onSuccess: () => {
      toast.success("Organization suspended")
      qc.invalidateQueries({ queryKey: ["organizations", projectId, status] })
      qc.invalidateQueries({
        queryKey: ["organizations", "count", projectId, status],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export function useUnsuspendOrganization(projectId: string, status: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (organizationID: string) =>
      OrganizationOpsService.UnsuspendOrganization(projectId, organizationID),
    onSuccess: () => {
      toast.success("Organization restored to active")
      qc.invalidateQueries({ queryKey: ["organizations", projectId, status] })
      qc.invalidateQueries({
        queryKey: ["organizations", "count", projectId, status],
      })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}

export { PAGE_SIZE }
