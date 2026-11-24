import { describe, expect, it } from "vitest"
import { humanizeAuditAction } from "./humanize-audit-action"

describe("humanizeAuditAction", () => {
  it("names a support change by what it touched", () => {
    expect(
      humanizeAuditAction(
        "studio.billing.subscriptions.update",
        "billing.subscriptions/x"
      )
    ).toBe("Subscription updated by support")
    expect(
      humanizeAuditAction(
        "studio.organization.organizations.update",
        "organization.organizations/x"
      )
    ).toBe("Organization updated by support")
    expect(
      humanizeAuditAction(
        "studio.messaging.outbox.insert",
        "messaging.outbox/x"
      )
    ).toBe("Action requested by support")
  })

  it("falls back to the raw action for an unknown support change", () => {
    expect(
      humanizeAuditAction("studio.billing.plans.update", "billing.plans/x")
    ).toBe("studio.billing.plans.update")
  })

  it("keeps mapping member requests by route", () => {
    expect(
      humanizeAuditAction("PATCH", "/organizations/:organizationID/settings")
    ).toBe("Updated settings")
  })
})
