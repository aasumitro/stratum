import { useMutation, type UseMutationOptions } from "@tanstack/react-query"
import { api } from "./axios"
import { catchHTTPError } from "./error"
import type { HTTPResponse } from "./response"

// Mutations whose server side makes a blocking payment-provider call
// (Stripe/Xendit) or works through a bulk import can outlast the default
// timeout while still succeeding on the server; an aborted request there
// invites a retry that duplicates the work.
export const PROVIDER_REQUEST_TIMEOUT_MS = 30_000

type UseHTTPMutationParams<TData, TVariables> = {
  url: string | ((variables: TVariables) => string)
  headers?:
    Record<string, string> | ((variables: TVariables) => Record<string, string>)
  /** Request timeout in ms, overriding the client-wide default. */
  timeout?: number
  options?: UseMutationOptions<
    HTTPResponse<TData>,
    HTTPResponse<unknown>,
    TVariables
  >
}

function postFn<TData, TVariables>(
  url: string | ((variables: TVariables) => string),
  headers?:
    | Record<string, string>
    | ((variables: TVariables) => Record<string, string>),
  timeout?: number
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url
      const resolvedHeaders =
        typeof headers === "function" ? headers(variables) : headers

      const res = await api.post<HTTPResponse<TData>>(resolvedUrl, variables, {
        headers: resolvedHeaders,
        timeout,
      })

      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionPost<TData, TVariables = void>({
  url,
  headers,
  timeout,
  options,
}: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: postFn<TData, TVariables>(url, headers, timeout),
    ...options,
  })
}

function deleteFn<TData, TVariables = void>(
  url: string | ((variables: TVariables) => string),
  headers?:
    Record<string, string> | ((variables: TVariables) => Record<string, string>)
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url
      const resolvedHeaders =
        typeof headers === "function" ? headers(variables) : headers

      const res = await api.delete<HTTPResponse<TData>>(resolvedUrl, {
        headers: resolvedHeaders,
      })

      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionDelete<TData, TVariables = void>({
  url,
  headers,
  options,
}: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: deleteFn<TData, TVariables>(url, headers),
    ...options,
  })
}

function patchFn<TData, TVariables>(
  url: string | ((variables: TVariables) => string),
  headers?:
    Record<string, string> | ((variables: TVariables) => Record<string, string>)
) {
  return async (variables: TVariables): Promise<HTTPResponse<TData>> => {
    try {
      const resolvedUrl = typeof url === "function" ? url(variables) : url
      const resolvedHeaders =
        typeof headers === "function" ? headers(variables) : headers
      const res = await api.patch<HTTPResponse<TData>>(resolvedUrl, variables, {
        headers: resolvedHeaders,
      })
      return res.data
    } catch (error) {
      throw catchHTTPError(error)
    }
  }
}

export function useHTTPActionPatch<TData, TVariables = void>({
  url,
  headers,
  options,
}: UseHTTPMutationParams<TData, TVariables>) {
  return useMutation({
    mutationFn: patchFn<TData, TVariables>(url, headers),
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
