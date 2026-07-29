import { test, expect, type Page } from "@playwright/test"
import { loginAsTestAccount } from "./helpers/login"

interface SubscriptionOverrides {
  cycle?: "monthly" | "yearly"
  max_extendable_months?: number
  hasPendingInvoice?: boolean
}

async function mockBillingAPIs(page: Page, sub: SubscriptionOverrides = {}) {
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
          cycle: sub.cycle ?? "monthly",
          status: "active",
          currency: "USD",
          period_end: "2027-01-01T00:00:00Z",
          max_extendable_months: sub.max_extendable_months ?? 24,
        },
        status: { error: false },
      }),
    })
  })

  await page.route(
    "**/v1/organizations/org-1/billing/invoices",
    async (route) => {
      if (route.request().method() !== "GET") return route.continue()
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: sub.hasPendingInvoice
            ? [
                {
                  id: "inv-pending-activation",
                  subscription_id: "sub-1",
                  amount_cents: 900,
                  tax_rate_bps: 0,
                  tax_cents: 0,
                  currency: "USD",
                  status: "pending",
                  kind: "activation",
                  switch_to_annual: false,
                  created_at: "2027-01-01T00:00:00Z",
                  updated_at: "2027-01-01T00:00:00Z",
                },
              ]
            : [],
          status: { error: false },
        }),
      })
    }
  )

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
        ],
        status: { error: false },
      }),
    })
  })
}

async function openExtendDialog(page: Page) {
  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /^extend$/i }).click()
}

test("plain extend happy path sends {months}, not switch_to_annual", async ({
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
  await page.route(
    "**/v1/organizations/org-1/billing/extend",
    async (route) => {
      captured.payload = route.request().postDataJSON()
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            id: "inv-1",
            amount_cents: 2700,
            currency: "USD",
            kind: "extension",
            switch_to_annual: false,
          },
          status: { error: false },
        }),
      })
    }
  )

  await openExtendDialog(page)

  // 1 -> 3 months via the +/- counter.
  await page.getByLabel(/increase months/i).click()
  await page.getByLabel(/increase months/i).click()
  await page.getByRole("button", { name: /continue/i }).click()

  await expect(
    page.getByRole("heading", { name: /review extension/i })
  ).toBeVisible()
  await page.getByRole("button", { name: /confirm/i }).click()

  expect(captured.payload).not.toBeNull()
  expect(captured.payload?.months).toBe(3)
  expect(captured.payload?.switch_to_annual).toBeUndefined()

  await expect(
    page.getByRole("heading", { name: /extension invoice created/i })
  ).toBeVisible()
})

test("switch to annual sends {switch_to_annual: true}, not months", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { cycle: "monthly", max_extendable_months: 24 })
  await loginAsTestAccount(page)

  const captured: { payload: Record<string, unknown> | null } = {
    payload: null,
  }
  await page.route(
    "**/v1/organizations/org-1/billing/extend",
    async (route) => {
      captured.payload = route.request().postDataJSON()
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            id: "inv-2",
            amount_cents: 9000,
            currency: "USD",
            kind: "extension",
            switch_to_annual: true,
          },
          status: { error: false },
        }),
      })
    }
  )

  await openExtendDialog(page)
  await page.getByRole("button", { name: /switch to annual/i }).click()

  await expect(
    page.getByRole("heading", { name: /review extension/i })
  ).toBeVisible()
  await expect(page.getByText(/billing cycle becomes yearly/i)).toBeVisible()

  await page.getByRole("button", { name: /confirm/i }).click()

  expect(captured.payload).not.toBeNull()
  expect(captured.payload?.switch_to_annual).toBe(true)
  expect(captured.payload?.months).toBeUndefined()

  await expect(
    page.getByRole("heading", { name: /extension invoice created/i })
  ).toBeVisible()
})

test("the +/- counter is capped at max_extendable_months, with a warning", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { max_extendable_months: 2 })
  await loginAsTestAccount(page)

  await openExtendDialog(page)

  const increaseBtn = page.getByLabel(/increase months/i)
  await increaseBtn.click() // 1 -> 2, the cap
  await expect(increaseBtn).toBeDisabled()
  await expect(page.getByText(/reached the maximum extension/i)).toBeVisible()
})

test("the Extend entry point is disabled when max_extendable_months is 0", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { max_extendable_months: 0 })
  await loginAsTestAccount(page)

  await page.goto("/organization/org-1/billing")
  await expect(page.getByRole("button", { name: /^extend$/i })).toBeDisabled()
})

test("switch to annual is hidden entirely on an already-yearly subscription", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { cycle: "yearly", max_extendable_months: 24 })
  await loginAsTestAccount(page)

  await openExtendDialog(page)
  await expect(
    page.getByRole("button", { name: /switch to annual/i })
  ).not.toBeVisible()
  // Not just hidden silently for the wrong reason — cycle==="yearly" omits
  // the section entirely, distinct from the "unavailable" note below.
  await expect(
    page.getByText(/switching to annual billing isn.t available/i)
  ).not.toBeVisible()
})

test("switch to annual shows an unavailable note (not a dead control) when max_extendable_months < 12", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { cycle: "monthly", max_extendable_months: 5 })
  await loginAsTestAccount(page)

  await openExtendDialog(page)
  await expect(
    page.getByRole("button", { name: /switch to annual/i })
  ).not.toBeVisible()
  await expect(
    page.getByText(/switching to annual billing isn.t available/i)
  ).toBeVisible()
})

test("a failed extend request stays on the review step", async ({ page }) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await page.route(
    "**/v1/organizations/org-1/billing/extend",
    async (route) => {
      return route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          status: { error: true, code: "EXTENSION_ALREADY_PENDING" },
        }),
      })
    }
  )

  await openExtendDialog(page)
  await page.getByRole("button", { name: /continue/i }).click()
  await expect(
    page.getByRole("heading", { name: /review extension/i })
  ).toBeVisible()
  await page.getByRole("button", { name: /confirm/i }).click()

  await expect(
    page.getByRole("heading", { name: /review extension/i })
  ).toBeVisible()
  await expect(
    page.getByRole("heading", { name: /extension invoice created/i })
  ).not.toBeVisible()
})

test("the Extend entry point is hidden while an invoice is still pending payment, even on an active subscription", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { hasPendingInvoice: true })
  await loginAsTestAccount(page)

  await page.goto("/organization/org-1/billing")
  await expect(page.getByRole("button", { name: /change plan/i })).toBeVisible()
  await expect(
    page.getByRole("button", { name: /^extend$/i })
  ).not.toBeVisible()
})
