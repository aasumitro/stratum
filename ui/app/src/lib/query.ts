import { QueryClient } from "@tanstack/react-query"
import { AxiosError } from "axios"
import { HTTP_STATUS_CODE } from "@/lib/api"

const DEV_MODE = import.meta.env.VITE_DEV_MODE

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        if (DEV_MODE) return false
        if (failureCount > 3) return false

        return !(
          error instanceof AxiosError &&
          error.response?.status === HTTP_STATUS_CODE.INTERNAL_SERVER_ERROR
        )
      },
      refetchOnWindowFocus: !DEV_MODE,
      staleTime: 2 * 60 * 1000,
    },
  },
})
