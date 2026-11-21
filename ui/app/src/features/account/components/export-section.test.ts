// @vitest-environment jsdom
//
// The retention sweep clears an export's result when it expires and keeps the task
// row, so "a completed export exists" is not enough to offer a download. Mocks the
// axios client, the HTTP-boundary convention the other render-level tests use.
import { createElement } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import "@/lib/i18n"

vi.mock("@/lib/api/axios", () => ({
  api: { get: vi.fn(), post: vi.fn() },
}))

import { api } from "@/lib/api/axios"
import { ExportSection } from "./export-section"
import type { UserTask } from "@/types/account"

function exportTask(result: unknown): UserTask {
  return {
    id: "task-1",
    kind: "export_data",
    status: "completed",
    result,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  }
}

// Renders with the given history and returns once the section has seen it, so an
// absent button means the section decided against it, not that it had yet to load.
async function renderSettled(tasks: UserTask[]) {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      data: tasks,
      status: { request_id: "req-1", error: false, message: "" },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    createElement(QueryClientProvider, { client }, createElement(ExportSection))
  )
  await waitFor(() => expect(client.isFetching()).toBe(0))
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

const downloadButton = () => screen.queryByRole("button", { name: "Download" })

describe("ExportSection", () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it("offers the download while the export still holds its result", async () => {
    await renderSettled([exportTask({ profile: {} })])

    expect(downloadButton()).not.toBeNull()
  })

  it("stops offering the download once the sweep cleared the result", async () => {
    await renderSettled([exportTask(null)])

    expect(downloadButton()).toBeNull()
    expect(screen.getByRole("button", { name: "Request export" })).toBeTruthy()
  })
})
