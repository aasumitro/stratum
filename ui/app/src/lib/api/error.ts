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
  // The HTTP status of the response, so callers can tell an outage (502/503/504)
  // from a refusal (4xx) even when the body carries no status object.
  readonly httpStatus?: number

  constructor(response: Partial<HTTPResponse<unknown>>, httpStatus?: number) {
    super(response.status?.message ?? "Request failed with server response")
    this.name = "ApiError"
    this.data = response.data
    this.status = response.status
    this.pagination = response.pagination
    this.next_cursor = response.next_cursor
    this.httpStatus = httpStatus
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
      throw new ApiError(
        data as Partial<HTTPResponse<unknown>>,
        error.response.status
      )
    }
    // No JSON body (a proxy's HTML page, an empty 502): keep the status, so
    // an outage is still told apart from a refusal.
    throw new ApiError({}, error.response.status)
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

// Gateway and unavailable statuses: the server, or something in front of it,
// could not answer this time.
const TRANSIENT_STATUSES = new Set([502, 503, 504])

// True for a failure that may clear on its own and is worth retrying: no
// response at all, or a gateway/unavailable status. A refusal (4xx) or a server
// bug (500) answers the same way every time.
export function isTransientError(err: unknown): boolean {
  if (isConnectionError(err)) return true
  return (
    err instanceof ApiError &&
    err.httpStatus !== undefined &&
    TRANSIENT_STATUSES.has(err.httpStatus)
  )
}

// Statuses that answer "no" about this caller or this resource: not signed in,
// not allowed, or not there. They mean the same on every retry.
const REFUSAL_STATUSES = new Set([401, 403, 404])

// True only when the server answered and refused (401/403/404). Anything else,
// an outage, a rate limit or a server error, says nothing about access or
// existence, so callers must not act on it as if it did.
export function isRefusal(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.httpStatus !== undefined &&
    REFUSAL_STATUSES.has(err.httpStatus)
  )
}
