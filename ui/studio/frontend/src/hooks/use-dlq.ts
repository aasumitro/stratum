import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { DLQService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export function useQueues(projectId: string) {
  return useQuery({
    queryKey: ["dlq", "queues", projectId],
    queryFn: () => DLQService.ListQueues(projectId),
    staleTime: 30_000,
    retry: 1,
  })
}

export function useMessages(projectId: string, queue: string | null) {
  return useQuery({
    queryKey: ["dlq", "messages", projectId, queue],
    queryFn: () => DLQService.GetMessages(projectId, queue!, 20),
    enabled: !!queue,
    staleTime: 15_000,
    retry: 1,
  })
}

export function useRequeueMessage(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (queue: string) => DLQService.RequeueMessage(projectId, queue),
    onSuccess: (result, queue) => {
      if (result?.requeued) {
        toast.success(`Requeued next message from "${queue}"`)
      }
      qc.invalidateQueries({ queryKey: ["dlq", "queues", projectId] })
      qc.invalidateQueries({ queryKey: ["dlq", "messages", projectId, queue] })
    },
    onError: (err) => {
      toast.error(`Requeue failed: ${wailsError(err)}`)
    },
  })
}

export function usePurgeQueue(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (queue: string) => DLQService.PurgeQueue(projectId, queue),
    onSuccess: (count, queue) => {
      toast.success(`Purged ${count} message${count !== 1 ? "s" : ""} from "${queue}"`)
      qc.invalidateQueries({ queryKey: ["dlq", "queues", projectId] })
      qc.invalidateQueries({ queryKey: ["dlq", "messages", projectId, queue] })
    },
    onError: (err) => {
      toast.error(`Purge failed: ${wailsError(err)}`)
    },
  })
}
