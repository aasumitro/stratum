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
          plan: "growth",
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
}

test("cancelling requires picking a reason before continuing", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /^cancel subscription$/i }).click()

  await expect(
    page.getByRole("heading", { name: /why are you cancelling/i })
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /continue/i })).toBeDisabled()
})

test("cancel with a named reason shows review then success", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  const captured: {
    payload: { reason?: string; details?: string } | null
  } = { payload: null }
  await page.route(
    "**/v1/organizations/org-1/billing/cancel",
    async (route) => {
      captured.payload = route.request().postDataJSON()
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: { error: false } }),
      })
    }
  )

  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /^cancel subscription$/i }).click()

  await page.getByRole("radio", { name: /it's too expensive/i }).click()
  await expect(page.getByText(/downgrading to a lower plan/i)).toBeVisible()
  await page.getByRole("button", { name: /continue/i }).click()

  // Review step: access-until disclosure + both export links present
  await expect(
    page.getByRole("heading", { name: /review cancellation/i })
  ).toBeVisible()
  await expect(page.getByText(/access continues until/i)).toBeVisible()
  await expect(
    page.getByRole("link", { name: /export my personal data/i })
  ).toHaveAttribute("href", "/account")
  await expect(
    page.getByRole("link", { name: /export organization data/i })
  ).toHaveAttribute("href", "/organization/org-1/audit-log")

  await page.getByRole("button", { name: /confirm cancellation/i }).click()

  // Request payload matches the backend DTO's field names exactly — a
  // frontend/backend field-name mismatch is exactly what a mocked e2e can
  // silently miss unless the real payload shape is asserted here.
  expect(captured.payload).not.toBeNull()
  expect(captured.payload?.reason).toBe("too_expensive")
  expect(captured.payload?.details).toBeUndefined()

  await expect(
    page.getByRole("heading", { name: /subscription cancelled/i })
  ).toBeVisible()
  await expect(
    page.getByRole("button", { name: /resume subscription/i })
  ).toBeVisible()
})

test("other reason sends the free-text details field", async ({ page }) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  const captured: {
    payload: { reason?: string; details?: string } | null
  } = { payload: null }
  await page.route(
    "**/v1/organizations/org-1/billing/cancel",
    async (route) => {
      captured.payload = route.request().postDataJSON()
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: { error: false } }),
      })
    }
  )

  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /^cancel subscription$/i }).click()

  await page.getByRole("radio", { name: /^other$/i }).click()
  await page
    .getByPlaceholder(/tell us more/i)
    .fill("Moving to a self-hosted alternative")
  await page.getByRole("button", { name: /continue/i }).click()
  await page.getByRole("button", { name: /confirm cancellation/i }).click()

  expect(captured.payload?.reason).toBe("other")
  expect(captured.payload?.details).toBe("Moving to a self-hosted alternative")
})

test("back from review preserves the picked reason", async ({ page }) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /^cancel subscription$/i }).click()

  await page.getByRole("radio", { name: /no longer need it/i }).click()
  await page.getByRole("button", { name: /continue/i }).click()
  await expect(
    page.getByRole("heading", { name: /review cancellation/i })
  ).toBeVisible()

  await page.getByRole("button", { name: /^back$/i }).click()
  await expect(
    page.getByRole("heading", { name: /why are you cancelling/i })
  ).toBeVisible()
  await expect(
    page.getByRole("radio", { name: /no longer need it/i })
  ).toBeChecked()
})

test("a failed cancel request stays on the review step", async ({ page }) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  await page.route(
    "**/v1/organizations/org-1/billing/cancel",
    async (route) => {
      return route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          status: { error: true, code: "SUBSCRIPTION_NOT_CANCELLABLE" },
        }),
      })
    }
  )

  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /^cancel subscription$/i }).click()

  await page.getByRole("radio", { name: /missing features/i }).click()
  await page.getByRole("button", { name: /continue/i }).click()
  await page.getByRole("button", { name: /confirm cancellation/i }).click()

  // Must not silently advance to the success step on a failed request.
  await expect(
    page.getByRole("heading", { name: /review cancellation/i })
  ).toBeVisible()
  await expect(
    page.getByRole("heading", { name: /subscription cancelled/i })
  ).not.toBeVisible()
})
