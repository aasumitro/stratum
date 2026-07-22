import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { MonitorService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export function useLastStatus(projectId: string, refetchInterval?: number) {
  return useQuery({
    queryKey: ["monitor", "last", projectId],
    queryFn: () => MonitorService.GetLastStatus(projectId),
    staleTime: 15_000,
    refetchInterval: refetchInterval ?? 30_000,
    retry: false,
  })
}

export function useHistory(projectId: string, limit = 10) {
  return useQuery({
    queryKey: ["monitor", "history", projectId, limit],
    queryFn: () => MonitorService.GetHistory(projectId, limit),
    staleTime: 10_000,
    refetchInterval: 15_000,
    retry: false,
  })
}

export function useIsPolling(projectId: string) {
  return useQuery({
    queryKey: ["monitor", "polling", projectId],
    queryFn: () => MonitorService.IsPolling(projectId),
    staleTime: 0,
    refetchInterval: 5_000,
    retry: false,
  })
}

export function useCheckHealth(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => MonitorService.CheckHealth(projectId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["monitor", "history", projectId] })
      qc.invalidateQueries({ queryKey: ["monitor", "last", projectId] })
    },
    onError: (err) => toast.error(`Health check failed: ${wailsError(err)}`),
  })
}

export function useStartPoller(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (intervalSeconds: number) =>
      MonitorService.StartPoller(projectId, intervalSeconds),
    onSuccess: () => {
      toast.success("Poller started")
      qc.invalidateQueries({ queryKey: ["monitor", "polling", projectId] })
    },
    onError: (err) => toast.error(`Failed to start poller: ${wailsError(err)}`),
  })
}

export function useStopPoller(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => MonitorService.StopPoller(projectId),
    onSuccess: () => {
      toast.success("Poller stopped")
      qc.invalidateQueries({ queryKey: ["monitor", "polling", projectId] })
    },
    onError: (err) => toast.error(`Failed to stop poller: ${wailsError(err)}`),
  })
}
