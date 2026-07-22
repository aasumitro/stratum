import axios from "axios"
import type { AxiosInstance } from "axios"
import { getCookie } from "@/lib/cookie"

const SESSION_COOKIE = "__session"

const DEV_MODE = import.meta.env.VITE_DEV_MODE
export const SERVER_URL =
  import.meta.env.VITE_SERVER_URL || "http://localhost:8000/api/v1"

export const HTTP_STATUS_CODE = {
  OK: 200,
  CREATED: 201,
  BAD_REQUEST: 400,
  UNAUTHORIZED: 401,
  UNPROCESSABLE_ENTITY: 422,
  INTERNAL_SERVER_ERROR: 500,
}

export const api: AxiosInstance = axios.create({
  baseURL: SERVER_URL,
  timeout: DEV_MODE ? 10000 : 5000,
})

if (DEV_MODE) {
  api.interceptors.request.use((config) => {
    console.log("[API Request]", config.method, config.url)
    return config
  })
}

// For FormData uploads, block dispatchRequest from setting Content-Type to
// 'application/x-www-form-urlencoded'. Setting it to null marks it as "present"
// (preventing the override) while null is filtered from serialized headers,
// letting the browser set the correct multipart/form-data boundary automatically.
api.interceptors.request.use((config) => {
  if (config.data instanceof FormData) {
    config.headers["Content-Type"] = null
  }
  return config
})

api.interceptors.request.use(async (config) => {
  const sessionToken = getCookie(SESSION_COOKIE)
  if (sessionToken) config.headers.Authorization = `Bearer ${sessionToken}`
  return config
})
