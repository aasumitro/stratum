import { useQuery } from "@tanstack/react-query"
import { OperatorLogService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export const OPERATOR_LOG_PAGE_SIZE = 50

export function useOperatorLog(
  projectId: string,
  limit = OPERATOR_LOG_PAGE_SIZE,
  offset = 0
) {
  return useQuery({
    queryKey: ["operator-log", "list", projectId, limit, offset],
    queryFn: () => OperatorLogService.List(projectId, limit, offset),
    staleTime: 10_000,
    retry: false,
    enabled: !!projectId,
  })
}

export function useOperatorLogCount(projectId: string) {
  return useQuery({
    queryKey: ["operator-log", "count", projectId],
    queryFn: () => OperatorLogService.Count(projectId),
    staleTime: 10_000,
    retry: false,
    enabled: !!projectId,
  })
}
