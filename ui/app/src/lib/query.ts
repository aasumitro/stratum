import { QueryClient } from "@tanstack/react-query"
import { isTransientError } from "@/lib/api/error"

const DEV_MODE = import.meta.env.VITE_DEV_MODE

// shouldRetryQuery retries a failed query up to three times, and only when the
// failure may clear on its own (isTransientError). Errors reach queries as
// ApiError / connection errors from catchHTTPError, never as a raw AxiosError.
export function shouldRetryQuery(
  failureCount: number,
  error: unknown
): boolean {
  return failureCount < 3 && isTransientError(error)
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) =>
        !DEV_MODE && shouldRetryQuery(failureCount, error),
      refetchOnWindowFocus: !DEV_MODE,
      staleTime: 2 * 60 * 1000,
    },
  },
})
