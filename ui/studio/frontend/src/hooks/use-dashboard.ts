import { useQuery } from "@tanstack/react-query"
import { DashboardService } from "../../bindings/github.com/aasumitro/stratum/studio/app"

export function useProjectMetrics(projectId: string) {
  return useQuery({
    queryKey: ["metrics", projectId],
    queryFn: () => DashboardService.GetMetrics(projectId),
    staleTime: 30_000,
    retry: 1,
  })
}

// Lightweight variant for project cards — silent on error, longer stale window
export function useCardMetrics(projectId: string) {
  return useQuery({
    queryKey: ["metrics", "card", projectId],
    queryFn: () => DashboardService.GetMetrics(projectId),
    staleTime: 60_000,
    retry: false,
    enabled: !!projectId,
  })
}
