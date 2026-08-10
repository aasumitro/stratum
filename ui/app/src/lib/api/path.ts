export const API = {
  me: (...rest: string[]) => `/v1/me${rest.length ? `/${rest.join("/")}` : ""}`,

  organizations: (id?: string, ...rest: string[]) =>
    id
      ? `/v1/organizations/${id}${rest.length ? `/${rest.join("/")}` : ""}`
      : "/v1/organizations",

  invitations: (...rest: string[]) =>
    `/v1/invitations${rest.length ? `/${rest.join("/")}` : ""}`,

  billing: (organizationId: string, ...rest: string[]) =>
    `/v1/organizations/${organizationId}/billing${rest.length ? `/${rest.join("/")}` : ""}`,

  // No organizationId — same eligibility check as billing()'s /coupons, for
  // the create-organization cart before an org exists.
  eligibleCouponsForNewOrg: () => "/v1/billing/coupons/eligible",

  references: (resource: string) => `/v1/references/${resource}`,
} as const
