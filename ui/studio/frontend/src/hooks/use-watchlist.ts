import { useQuery } from "@tanstack/react-query"
import { WatchlistService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"
import type { WatchlistResult } from "../../bindings/github.com/aasumitro/stratum/studio/app/models.js"

export function useWatchlist(projectId: string) {
  return useQuery({
    queryKey: ["watchlist", projectId],
    queryFn: () => WatchlistService.GetAll(projectId),
    staleTime: 60_000,
    retry: false,
    enabled: !!projectId,
  })
}

export function urgentCount(result: WatchlistResult | undefined): number {
  if (!result) return 0
  return (
    (result.trials_ending_soon?.length ?? 0) + (result.past_due?.length ?? 0)
  )
}
