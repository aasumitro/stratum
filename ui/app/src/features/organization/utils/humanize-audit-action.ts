// Human verbs everywhere; raw method+path only in the details drawer.
// `resource` is Gin's route pattern (e.g. "/organizations/:organizationID/members"),
// not the literal request path — matched against known patterns. Falls back
// to the raw HTTP method when nothing matches, which is honest about what
// wasn't mapped rather than guessing.
const PATTERNS: { test: RegExp; method?: string; label: string }[] = [
  { test: /\/invitations$/, method: "POST", label: "Invited member" },
  {
    test: /\/invitations\/:invitationID$/,
    method: "DELETE",
    label: "Revoked invitation",
  },
  { test: /\/members\/import$/, method: "POST", label: "Imported members" },
  { test: /\/members$/, method: "POST", label: "Added member" },
  {
    test: /\/members\/:authSub\/role$/,
    method: "PATCH",
    label: "Changed member role",
  },
  { test: /\/members\/:authSub$/, method: "DELETE", label: "Removed member" },
  { test: /\/leave$/, method: "DELETE", label: "Left organization" },
  { test: /\/transfer$/, method: "POST", label: "Transferred ownership" },
  { test: /\/settings$/, method: "PATCH", label: "Updated settings" },
  {
    test: /^\/organizations\/:organizationID$/,
    method: "PATCH",
    label: "Updated organization",
  },
  {
    test: /^\/organizations\/:organizationID$/,
    method: "DELETE",
    label: "Deleted organization",
  },
  { test: /\/invite-code$/, method: "POST", label: "Regenerated invite code" },
  { test: /\/invite-code$/, method: "PATCH", label: "Toggled invite code" },
  { test: /\/webhooks$/, method: "POST", label: "Created webhook" },
  {
    test: /\/webhooks\/:webhookID$/,
    method: "PATCH",
    label: "Updated webhook",
  },
  {
    test: /\/webhooks\/:webhookID$/,
    method: "DELETE",
    label: "Deleted webhook",
  },
  {
    test: /\/deliveries\/:deliveryID\/retry$/,
    method: "POST",
    label: "Retried webhook delivery",
  },
  { test: /\/logo$/, method: "POST", label: "Uploaded logo" },
  { test: /\/files$/, method: "POST", label: "Uploaded file" },
  { test: /\/files\/:fileID$/, method: "DELETE", label: "Deleted file" },
  { test: /\/billing\/plan$/, method: "PATCH", label: "Changed plan" },
  {
    test: /\/billing\/cancel$/,
    method: "POST",
    label: "Cancelled subscription",
  },
  { test: /\/billing\/resume$/, method: "POST", label: "Resumed subscription" },
  { test: /\/billing\/addons$/, method: "POST", label: "Attached add-on" },
  {
    test: /\/billing\/addons\/:addonID$/,
    method: "DELETE",
    label: "Detached add-on",
  },
  {
    test: /\/billing\/coupons\/redeem$/,
    method: "POST",
    label: "Redeemed coupon",
  },
  { test: /\/audit-log\/export$/, label: "Exported audit log" },
]

// A change made by the provider's support team in Studio is recorded as
// "studio.<schema>.<table>.<insert|update|delete>" on the row it touched.
const OPERATOR_SUBJECTS: Record<string, string> = {
  organizations: "Organization",
  subscriptions: "Subscription",
  subscription_addons: "Add-on",
  coupon_redemptions: "Coupon",
  invoices: "Invoice",
  payment_links: "Payment link",
  payments: "Payment",
}
const OPERATOR_VERBS: Record<string, string> = {
  insert: "created",
  update: "updated",
  delete: "removed",
}

function humanizeOperatorAction(action: string): string | undefined {
  const [, , table, op] = action.split(".")
  if (table === "outbox") return "Action requested by support"
  const subject = OPERATOR_SUBJECTS[table]
  const verb = OPERATOR_VERBS[op]
  return subject && verb ? `${subject} ${verb} by support` : undefined
}

export function humanizeAuditAction(action: string, resource: string): string {
  if (action.startsWith("studio.")) {
    return humanizeOperatorAction(action) ?? action
  }
  for (const p of PATTERNS) {
    if ((!p.method || p.method === action) && p.test.test(resource))
      return p.label
  }
  return action
}
