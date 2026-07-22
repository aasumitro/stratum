import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { BroadcastService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"
import type { BroadcastInput } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export function useBroadcastHistory(projectId: string) {
  return useQuery({
    queryKey: ["broadcast", "history", projectId],
    queryFn: () => BroadcastService.GetHistory(projectId),
    staleTime: 30_000,
    retry: 1,
  })
}

export function useCountRecipients(projectId: string, target: string) {
  return useQuery({
    queryKey: ["broadcast", "count", projectId, target],
    queryFn: () => BroadcastService.CountRecipients(projectId, target),
    enabled: !!target,
    staleTime: 60_000,
    retry: 1,
  })
}

export function useSendBroadcast(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: BroadcastInput) => BroadcastService.Send(projectId, input),
    onSuccess: (count) => {
      toast.success(`Broadcast sent to ${count} recipient${count === 1 ? "" : "s"}`)
      qc.invalidateQueries({ queryKey: ["broadcast", "history", projectId] })
    },
    onError: (err) => toast.error(`Failed: ${wailsError(err)}`),
  })
}
