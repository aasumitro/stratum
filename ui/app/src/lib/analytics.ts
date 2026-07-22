// minimal posthog shim — swap body for posthog-js SDK if full feature set is needed
export function capture(event: string, properties?: Record<string, unknown>) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(window as any).posthog?.capture?.(event, properties)
}
