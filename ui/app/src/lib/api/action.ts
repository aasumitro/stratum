import { useMutation, type UseMutationOptions } from "@tanstack/react-query"
import { api } from "./axios"
import { catchHTTPError } from "./error"
import type { HTTPResponse } from "./response"

type UseHTTPMutationParams<TData, TVariables> = {
  url: string | ((variables: TVariables) => string)
  options?: UseMutationOptions<
    HTTPResponse<TData>,
    HTTPResponse<unknown>,
    TVariables
  >
}

function postFn<TData, TVariables>(
  url: string | ((variables: TVariables) => string)
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url

      const res = await api.post<HTTPResponse<TData>>(resolvedUrl, variables)

      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

function putFn<TData, TVariables extends { id?: number; data?: unknown }>(
  url: string | ((variables: TVariables) => string)
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url

      const body = "data" in variables ? variables.data : variables

      const res = await api.put<HTTPResponse<TData>>(resolvedUrl, body)

      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionPost<TData, TVariables = void>({
  url,
  options,
}: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: postFn<TData, TVariables>(url),
    ...options,
  })
}

export function useHTTPActionPut<
  TData,
  TVariables extends { id?: number; data?: unknown } = {
    id?: number
    data?: unknown
  },
>({ url, options }: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: putFn<TData, TVariables>(url),
    ...options,
  })
}

function deleteFn<TData, TVariables = void>(
  url: string | ((variables: TVariables) => string)
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url

      const res = await api.delete<HTTPResponse<TData>>(resolvedUrl)

      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionDelete<TData, TVariables = void>({
  url,
  options,
}: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: deleteFn<TData, TVariables>(url),
    ...options,
  })
}

function patchFn<TData, TVariables>(
  url: string | ((variables: TVariables) => string)
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url
      const res = await api.patch<HTTPResponse<TData>>(resolvedUrl, variables)
      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionPatch<TData, TVariables = void>({
  url,
  options,
}: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: patchFn<TData, TVariables>(url),
    ...options,
  })
}

function uploadFn<TData>(url: string, fieldName: string) {
  return async (file: File): Promise<HTTPResponse<TData>> => {
    try {
      const form = new FormData()
      form.append(fieldName, file)
      // No timeout — the shared `api` instance's 5s default is sized for
      // JSON requests, not a 50MB file transfer.
      const res = await api.post<HTTPResponse<TData>>(url, form, {
        timeout: 0,
      })
      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionUpload<TData>({
  url,
  fieldName,
  options,
}: {
  url: string
  fieldName: string
  options?: UseMutationOptions<HTTPResponse<TData>, HTTPResponse<unknown>, File>
}) {
  return useMutation({
    mutationFn: uploadFn<TData>(url, fieldName),
    ...options,
  })
}
