import { useQuery, type UseQueryOptions } from "@tanstack/react-query"
import { api } from "./axios"
import { catchHTTPError } from "./error"
import type { HTTPResponse } from "./response"

export type QueryFilter = Record<string, never | undefined>

export type QueryOption = { enabled?: boolean; id?: string }

type UseHTTPQueryParams<T> = {
  url: string
  queryKey: readonly unknown[]
  options?: Omit<
    UseQueryOptions<HTTPResponse<T>, HTTPResponse<unknown>>,
    "queryKey" | "queryFn"
  >
}

export function getFn<T>(url: string) {
  return async (): Promise<HTTPResponse<T>> => {
    try {
      const res = await api.get<HTTPResponse<T>>(url)
      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPQuery<T>({
  queryKey,
  url,
  options,
}: UseHTTPQueryParams<T>) {
  return useQuery({
    queryKey,
    queryFn: getFn<T>(url),
    ...options,
  })
}
