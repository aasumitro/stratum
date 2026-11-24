import type { Notification } from "@/types/notification"

// A run of notifications of the same kind for the same organization within a
// day collapses into one row: the newest leads, the rest fold under it.
export interface NotificationRun {
  lead: Notification
  rest: Notification[]
}

const RUN_WINDOW_MS = 24 * 60 * 60 * 1000

// collapseRuns folds consecutive items (newest first, as the feed returns
// them) into runs; only neighbours join, so the timeline order is kept.
export function collapseRuns(items: Notification[]): NotificationRun[] {
  const out: NotificationRun[] = []
  for (const n of items) {
    const last = out[out.length - 1]
    if (
      last &&
      last.lead.channel === n.channel &&
      last.lead.organization_id === n.organization_id &&
      Date.parse(last.lead.created_at) - Date.parse(n.created_at) <=
        RUN_WINDOW_MS
    ) {
      last.rest.push(n)
    } else {
      out.push({ lead: n, rest: [] })
    }
  }
  return out
}

// A run is open when any of its items was open: items joining it at either
// end (a newer one at the top, older ones from "Load more") keep it open.
export function runIds(run: NotificationRun): string[] {
  return [run.lead.id, ...run.rest.map((x) => x.id)]
}

export function isRunOpen(run: NotificationRun, open: Set<string>): boolean {
  return runIds(run).some((id) => open.has(id))
}

// toggleRun opens a run by remembering all its items, or closes it by
// forgetting them.
export function toggleRun(
  run: NotificationRun,
  open: Set<string>
): Set<string> {
  const next = new Set(open)
  const ids = runIds(run)
  if (isRunOpen(run, open)) ids.forEach((id) => next.delete(id))
  else ids.forEach((id) => next.add(id))
  return next
}

// chunk splits ids into requests the API accepts (at most size each).
export function chunk<T>(items: T[], size: number): T[][] {
  const out: T[][] = []
  for (let i = 0; i < items.length; i += size)
    out.push(items.slice(i, i + size))
  return out
}
