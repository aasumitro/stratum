import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ConnectionService,
  ProjectService,
} from "../../bindings/github.com/aasumitro/stratum/studio/app"
import type { ProjectInput } from "../../bindings/github.com/aasumitro/stratum/studio/app/models.js"

export function useProjects() {
  return useQuery({
    queryKey: ["projects"],
    queryFn: () => ProjectService.List(),
    select: (data) => data ?? [],
  })
}

export function useAddProject() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: ProjectInput) => ProjectService.Add(input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["projects"] }),
  })
}

export function useUpdateProject() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: ProjectInput }) =>
      ProjectService.Update(id, input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["projects"] }),
  })
}

export function useDeleteProject() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => ProjectService.Delete(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["projects"] }),
  })
}

export function useTestConnection() {
  return useMutation({
    mutationFn: ({
      type,
      dsn,
    }: {
      type: "database" | "mq" | "redis"
      dsn: string
    }) => {
      if (type === "database") return ConnectionService.TestDatabase(dsn)
      if (type === "mq") return ConnectionService.TestMQ(dsn)
      return ConnectionService.TestRedis(dsn)
    },
  })
}
