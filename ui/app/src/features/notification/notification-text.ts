import type { Notification } from "@/types/notification"

// i18next leaves a placeholder in place when the payload has no value for it.
const UNFILLED_PLACEHOLDER = /\{\{[^}]*\}\}/

// Translated feed copy from payload + the notifications.messages i18n
// catalog (en.json/id.json), so switching language retranslates the whole
// feed live — falls back to the raw stored subject/body when the catalog
// doesn't cover the channel yet, or when the row's payload lacks a value
// the catalog copy needs (rows written before payload existed). A channel
// whose copy has no placeholders translates even with an empty payload.
// That fallback is what lets backend/frontend ship this independently, in
// either order.
export function notificationDisplayText(
  n: Notification,
  t: (key: string, options?: Record<string, unknown>) => string,
  exists: (key: string) => boolean
): { subject: string; body: string } {
  const payload = n.payload ?? {}
  let key = n.channel
  if (key === "subscription_remind" && payload.is_trial) {
    key = "subscription_remind_trial"
  } else if (key === "invoice_created" && payload.from_trial) {
    key = "invoice_created_trial"
  }
  if (!exists(`notifications.messages.${key}.body`)) {
    return { subject: n.subject, body: n.body }
  }
  const subject = t(`notifications.messages.${key}.title`, payload)
  const body = t(`notifications.messages.${key}.body`, payload)
  if (UNFILLED_PLACEHOLDER.test(subject) || UNFILLED_PLACEHOLDER.test(body)) {
    return { subject: n.subject, body: n.body }
  }
  return { subject, body }
}
