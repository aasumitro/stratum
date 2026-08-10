import { test, expect, type Page } from "@playwright/test"
import { loginAsTestAccount } from "./helpers/login"

async function mockBillingAPIs(page: Page) {
  // Wildcard fallback to prevent "Unable to connect"
  await page.route("**/v1/**", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [], status: { error: false } }),
    })
  })

  await page.route("**/v1/me", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          id: "e2e-user",
          auth_sub: "e2e-auth-sub",
          email: "e2e@example.com",
          full_name: "E2E User",
          avatar_url: "",
          preferences: { onboarding_completed: true },
        },
        status: { error: false },
        pagination: null,
      }),
    })
  })

  await page.route("**/v1/organizations", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: "org-1", name: "E2E Org", slug: "e2e-org", role: "owner" },
        ],
        status: { error: false },
        pagination: null,
      }),
    })
  })

  await page.route("**/v1/organizations/org-1/billing", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          plan: "solo",
          cycle: "monthly",
          status: "active",
          currency: "USD",
          period_end: "2027-01-01T00:00:00Z",
          max_extendable_months: 24,
        },
        status: { error: false },
      }),
    })
  })

  await page.route("**/v1/references/plans*", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "solo",
            name: "Solo",
            sort_order: 10,
            active: true,
            features: [],
            limits: { members: 5 },
            prices: { USD: { monthly: 900, yearly: 9000 } },
          },
          {
            id: "growth",
            name: "Growth",
            sort_order: 20,
            active: true,
            features: ["priority_support", "advanced_analytics"],
            limits: { members: 15, workspaces: 25 },
            prices: { USD: { monthly: 2900, yearly: 29000 } },
          },
        ],
        status: { error: false },
      }),
    })
  })

  await page.route("**/v1/references/features*", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "priority_support",
            name: "Priority Support",
            type: "boolean",
            active: true,
          },
          {
            id: "advanced_analytics",
            name: "Advanced Analytics",
            type: "boolean",
            active: true,
          },
          {
            id: "members",
            name: "Members",
            type: "metered",
            metric_key: "members",
            active: true,
          },
          {
            id: "workspaces",
            name: "Workspaces",
            type: "metered",
            metric_key: "workspaces",
            active: true,
          },
        ],
        status: { error: false },
      }),
    })
  })

  await page.route(
    "**/v1/organizations/org-1/billing/preview*",
    async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            plan: "growth",
            cycle: "monthly",
            currency: "USD",
            plan_line_cents: 2900,
            total_cents: 2900,
          },
          status: { error: false },
        }),
      })
    }
  )
}

async function openPlanSelector(page: Page) {
  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /change plan/i }).click()
}

test("upgrading to a higher plan shows new features then requires terms agreement", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  const captured: { payload: Record<string, unknown> | null } = {
    payload: null,
  }
  await page.route("**/v1/organizations/org-1/billing/plan", async (route) => {
    captured.payload = route.request().postDataJSON()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: { error: false } }),
    })
  })

  await openPlanSelector(page)
  await page.getByLabel(/Growth/).click()
  await page.getByRole("button", { name: /continue/i }).click()

  // "What Changes" step shows the new features unlocked by Growth. Scoped
  // to the dialog since the page behind it also renders these feature
  // names in the always-visible Plan features section.
  const changesDialog = page.getByRole("dialog")
  await expect(
    changesDialog.getByRole("heading", { name: /what changes/i })
  ).toBeVisible()
  await expect(changesDialog.getByText(/priority support/i)).toBeVisible()
  await expect(changesDialog.getByText(/advanced analytics/i)).toBeVisible()
  // Limits use the real feature catalog's name, not the plan's raw limit
  // key — must render as "Workspaces: 25", not an untranslated
  // "workspaces: 25" or the raw key itself.
  await expect(changesDialog.getByText(/workspaces: 25/i)).toBeVisible()
  await expect(changesDialog.getByText(/members: 15/i)).toBeVisible()

  await page.getByRole("button", { name: /continue/i }).click()

  // Review step: Confirm is disabled until the terms checkbox is checked.
  await expect(
    page.getByRole("heading", { name: /confirm plan change/i })
  ).toBeVisible()
  const confirmBtn = page.getByRole("button", { name: /confirm change/i })
  await expect(confirmBtn).toBeDisabled()

  await page
    .getByRole("checkbox", { name: /agree to the billing terms/i })
    .click()
  await expect(confirmBtn).toBeEnabled()
  await confirmBtn.click()

  expect(captured.payload).not.toBeNull()
  expect(captured.payload?.plan).toBe("growth")
  expect(captured.payload?.cycle).toBe("monthly")
  expect(captured.payload?.terms_agreed).toBe(true)

  await expect(
    page.getByRole("heading", { name: /plan changed/i })
  ).toBeVisible()
})

test("a cycle-only change shows the cycle comparison, not an empty feature list", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await openPlanSelector(page)
  // Same plan (Solo), switch to yearly.
  await page.getByRole("button", { name: /^yearly$/i }).click()
  await page.getByRole("button", { name: /continue/i }).click()

  const changesDialog = page.getByRole("dialog")
  await expect(
    changesDialog.getByRole("heading", { name: /what changes/i })
  ).toBeVisible()
  await expect(changesDialog.getByText(/billing cycle/i)).toBeVisible()
  await expect(
    changesDialog.getByText(/features and limits stay/i)
  ).toBeVisible()
  await expect(changesDialog.getByText(/priority support/i)).not.toBeVisible()
})

test("changing both plan and cycle at once shows both blocks together", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await openPlanSelector(page)
  // Solo monthly -> Growth yearly: both dimensions change in one confirm.
  await page.getByLabel(/Growth/).click()
  await page.getByRole("button", { name: /^yearly$/i }).click()
  await page.getByRole("button", { name: /continue/i }).click()

  const changesDialog = page.getByRole("dialog")
  await expect(
    changesDialog.getByRole("heading", { name: /what changes/i })
  ).toBeVisible()
  // Plan-tier block present.
  await expect(changesDialog.getByText(/priority support/i)).toBeVisible()
  await expect(changesDialog.getByText(/advanced analytics/i)).toBeVisible()
  // Cycle block present too — not dropped in favor of the plan block.
  await expect(changesDialog.getByText(/billing cycle/i)).toBeVisible()
  await expect(
    changesDialog.getByText(/switching from monthly to yearly/i)
  ).toBeVisible()
  // "Features unchanged" only applies to a pure cycle-only change — must
  // not show here since the plan itself changed too.
  await expect(
    changesDialog.getByText(/features and limits stay/i)
  ).not.toBeVisible()
})

test("a failed plan change stays on the review step", async ({ page }) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await page.route("**/v1/organizations/org-1/billing/plan", async (route) => {
    return route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        status: { error: true, code: "UNKNOWN_PLAN" },
      }),
    })
  })

  await openPlanSelector(page)
  await page.getByLabel(/Growth/).click()
  await page.getByRole("button", { name: /continue/i }).click()
  await page.getByRole("button", { name: /continue/i }).click()
  await page
    .getByRole("checkbox", { name: /agree to the billing terms/i })
    .click()
  await page.getByRole("button", { name: /confirm change/i }).click()

  await expect(
    page.getByRole("heading", { name: /confirm plan change/i })
  ).toBeVisible()
  await expect(
    page.getByRole("heading", { name: /plan changed/i })
  ).not.toBeVisible()
})
