// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest"
import i18n from "@/lib/i18n" // side-effect: initializes the global i18next instance
import type { Notification } from "@/types/notification"
import { notificationDisplayText } from "./notification-text"

function baseNotification(overrides: Partial<Notification>): Notification {
  return {
    id: "n1",
    organization_id: "org-1",
    kind: "in_app",
    channel: "invoice_paid",
    subject: "raw fallback subject",
    body: "raw fallback body",
    payload: {},
    status: "sent",
    created_at: "2026-08-06T00:00:00Z",
    updated_at: "2026-08-06T00:00:00Z",
    ...overrides,
  }
}

describe("notificationDisplayText", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en")
  })

  it("interpolates payload into the EN catalog string", () => {
    const n = baseNotification({
      channel: "invoice_paid",
      payload: { invoice_id: "inv-42" },
    })
    const { subject, body } = notificationDisplayText(
      n,
      i18n.t.bind(i18n),
      i18n.exists.bind(i18n)
    )
    expect(subject).toBe("Invoice paid")
    expect(body).toBe("Invoice inv-42 has been paid successfully.")
    expect(body).not.toContain("{{")
  })

  it("interpolates payload into the ID catalog string after switching language", async () => {
    await i18n.changeLanguage("id")
    const n = baseNotification({
      channel: "invoice_paid",
      payload: { invoice_id: "inv-42" },
    })
    const { body } = notificationDisplayText(
      n,
      i18n.t.bind(i18n),
      i18n.exists.bind(i18n)
    )
    expect(body).toBe("Tagihan inv-42 telah berhasil dibayar.")
  })

  it("picks the trial-variant catalog key when payload.is_trial is true", () => {
    const n = baseNotification({
      channel: "subscription_remind",
      payload: { expected_end: "Aug 10, 2026", is_trial: true },
    })
    const { body } = notificationDisplayText(
      n,
      i18n.t.bind(i18n),
      i18n.exists.bind(i18n)
    )
    expect(body).toContain("free trial")
  })

  it("falls back to raw subject/body when payload is empty (pre-payload row)", () => {
    const n = baseNotification({ payload: {} })
    const result = notificationDisplayText(
      n,
      i18n.t.bind(i18n),
      i18n.exists.bind(i18n)
    )
    expect(result).toEqual({ subject: n.subject, body: n.body })
  })

  it("falls back to raw subject/body when the channel has no catalog entry", () => {
    const n = baseNotification({
      channel: "not_a_real_channel",
      payload: { foo: "bar" },
    })
    const result = notificationDisplayText(
      n,
      i18n.t.bind(i18n),
      i18n.exists.bind(i18n)
    )
    expect(result).toEqual({ subject: n.subject, body: n.body })
  })
})
