export const queryKeys = {
  account: {
    me: () => ["account", "me"] as const,
    tasks: () => ["account", "tasks"] as const,
    task: (id: string) => ["account", "task", id] as const,
    sessions: (cursor?: string) => ["account", "sessions", cursor] as const,
    notifications: (organizationId?: string) =>
      organizationId
        ? (["account", "notifications", organizationId] as const)
        : (["account", "notifications"] as const),
    notificationCount: (organizationId?: string) =>
      organizationId
        ? ([
            "account",
            "notifications",
            organizationId,
            "unread-count",
          ] as const)
        : (["account", "notifications", "unread-count"] as const),
    notificationPreferences: () =>
      ["account", "notification-preferences"] as const,
    mfaFactors: () => ["account", "mfa-factors"] as const,
    auditLog: () => ["account", "audit-log"] as const,
    myInvitations: () => ["account", "invitations"] as const,
  },
  organizations: {
    list: () => ["organizations"] as const,
    detail: (id: string) => ["organizations", id] as const,
    members: (id: string) => ["organizations", id, "members"] as const,
    invitations: (id: string) => ["organizations", id, "invitations"] as const,
    auditLog: (id: string) => ["organizations", id, "audit-log"] as const,
    webhooks: (id: string) => ["organizations", id, "webhooks"] as const,
    webhookDeliveries: (id: string, webhookId: string) =>
      ["organizations", id, "webhooks", webhookId, "deliveries"] as const,
  },
  billing: {
    subscription: (wsId: string) => ["billing", wsId, "subscription"] as const,
    plans: () => ["billing", "plans"] as const,
    invoices: (wsId: string) => ["billing", wsId, "invoices"] as const,
    payments: (wsId: string) => ["billing", wsId, "payments"] as const,
    paymentLinks: (wsId: string) => ["billing", wsId, "payment-links"] as const,
    usage: (wsId: string) => ["billing", wsId, "usage"] as const,
    history: (wsId: string) => ["billing", wsId, "history"] as const,
    addons: (wsId: string) => ["billing", wsId, "addons"] as const,
    // Prefix shared by every useInvoicePreview variant (plan/cycle appended
    // when previewing a hypothetical change) — invalidating this prefix
    // covers all of them without needing to know which variant is cached.
    preview: (wsId: string) =>
      ["billing", wsId, "subscription", "preview"] as const,
    eligibleCouponsForNewOrg: () =>
      ["billing", "eligible-coupons-new-org"] as const,
  },
  references: {
    countries: () => ["references", "countries"] as const,
    plans: () => ["references", "plans"] as const,
    features: () => ["references", "features"] as const,
    addons: () => ["references", "addons"] as const,
  },
} as const
