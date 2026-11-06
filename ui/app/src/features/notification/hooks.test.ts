// @vitest-environment jsdom
//
// useNotificationStream only needs a QueryClient; EventSource is replaced
// with a fake whose instances the test drives by hand, and fake timers run
// the hook's reconnect backoff without waiting for it.
import { createElement, type ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { useNotificationStream } from "./hooks"

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener() {}
  close() {}
}

function latest(): FakeEventSource {
  const es = FakeEventSource.instances.at(-1)
  if (!es) throw new Error("no EventSource opened")
  return es
}

function wrapper({ children }: { children: ReactNode }) {
  return createElement(
    QueryClientProvider,
    { client: new QueryClient() },
    children
  )
}

beforeEach(() => {
  vi.useFakeTimers()
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe("useNotificationStream", () => {
  it("keeps reconnecting when every drop follows a successful open", () => {
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval")
    renderHook(() => useNotificationStream(), { wrapper })

    for (let i = 0; i < 5; i++) {
      latest().onopen?.()
      latest().onerror?.()
      vi.runOnlyPendingTimers()
    }

    expect(FakeEventSource.instances).toHaveLength(6)
    expect(setIntervalSpy).not.toHaveBeenCalled()
  })

  it("falls back to polling after three consecutive failed connects", () => {
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval")
    renderHook(() => useNotificationStream(), { wrapper })

    for (let i = 0; i < 3; i++) {
      latest().onerror?.()
      vi.runOnlyPendingTimers()
    }

    expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), 60_000)
    expect(FakeEventSource.instances).toHaveLength(3)
  })
})
