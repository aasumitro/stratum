import axios, { AxiosError } from "axios"
import { toast } from "sonner"
import i18n from "@/lib/i18n"
import type {
  HTTPResponse,
  ResponsePagination,
  ResponseStatus,
} from "./response"

// Real Error instance carrying the backend's HTTPResponse envelope, so
// `instanceof Error` / `.stack` work while `.status.code` / `.status.message`
// keep working for every existing call site that reads them off the raw shape.
export class ApiError extends Error {
  readonly data: unknown
  readonly status?: ResponseStatus
  readonly pagination?: ResponsePagination
  readonly next_cursor?: string

  constructor(response: Partial<HTTPResponse<unknown>>) {
    super(response.status?.message ?? "Request failed with server response")
    this.name = "ApiError"
    this.data = response.data
    this.status = response.status
    this.pagination = response.pagination
    this.next_cursor = response.next_cursor
  }
}

export function isHTTPResponse<T>(obj: unknown): obj is HTTPResponse<T> {
  return typeof obj === "object" && obj !== null && "status" in obj
}

export const SERVER_ERROR = {
  CONNECTION:
    "Unable to connect to the server. " +
    "Please check your internet and try again. If the issue persists, try again later.",
  FORM: "invalid form parameters",
}

export function parseApiError(err: unknown, fallback: string): string {
  const e = err as {
    response?: { data?: { status?: { code?: string; message?: string } } }
    status?: { code?: string; message?: string }
  }
  const status = e?.status ?? e?.response?.data?.status
  const message = status?.message ?? fallback
  if (!status?.code) return message
  return i18n.t(`errors.codes.${status.code}`, { defaultValue: message })
}

export const catchHTTPError = (error: unknown) => {
  // Axios error with response (backend error)
  if (error instanceof AxiosError && error.response) {
    const data = error.response.data
    // If backend returns your HTTPResponse shape
    if (data && typeof data === "object") {
      throw new ApiError(data as Partial<HTTPResponse<unknown>>)
    }
    throw new Error("Request failed with server response")
  }

  // Axios error without response (network / CORS / timeout)
  if (axios.isAxiosError(error) && !error.response) {
    throw new Error(SERVER_ERROR.CONNECTION, error)
  }

  // Native JS error
  if (error instanceof Error) {
    throw error
  }

  throw new Error("Unexpected error occurred")
}

export function handleHttpError(error: unknown): void {
  toast.error(
    parseApiError(error, "An unexpected error occurred. Please try again.")
  )
}

// True when catchHTTPError classified this as "couldn't reach the server at
// all" (network/CORS/timeout) — as opposed to a real HTTP error response
// (404, 422, etc.). Route guards use this to avoid mistaking "server is
// down" for "resource doesn't exist yet".
export function isConnectionError(err: unknown): boolean {
  return err instanceof Error && err.message === SERVER_ERROR.CONNECTION
}
