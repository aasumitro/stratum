import type { Notification } from "@/types/notification"

// Translated feed copy from payload + the notifications.messages i18n
// catalog (en.json/id.json), so switching language retranslates the whole
// feed live — falls back to the raw stored subject/body for rows written
// before payload existed, or for a channel the catalog doesn't cover yet.
// That fallback is what lets backend/frontend ship this independently, in
// either order.
export function notificationDisplayText(
  n: Notification,
  t: (key: string, options?: Record<string, unknown>) => string,
  exists: (key: string) => boolean
): { subject: string; body: string } {
  if (!n.payload || Object.keys(n.payload).length === 0) {
    return { subject: n.subject, body: n.body }
  }
  let key = n.channel
  if (key === "subscription_remind" && n.payload.is_trial) {
    key = "subscription_remind_trial"
  } else if (key === "invoice_created" && n.payload.from_trial) {
    key = "invoice_created_trial"
  }
  if (!exists(`notifications.messages.${key}.body`)) {
    return { subject: n.subject, body: n.body }
  }
  return {
    subject: t(`notifications.messages.${key}.title`, n.payload),
    body: t(`notifications.messages.${key}.body`, n.payload),
  }
}
