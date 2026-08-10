export interface HTTPResponse<T> {
  data: T | null
  status: ResponseStatus
  pagination: ResponsePagination
  next_cursor?: string
}

export interface ResponseStatus {
  request_id: string
  error: boolean
  code?: string
  message: string
  details:
    | string
    | string[]
    | Record<string, { code: string; param?: string }[]>
    | Record<string, string>
}

export interface ResponsePagination {
  limit: number
  offset: number
  current_page: number
  total_pages: number
  total_items: number
}
