import { test, expect, type Page } from "@playwright/test"
import { loginAsTestAccount } from "./helpers/login"

async function mockBillingAPIs(
  page: Page,
  opts: { status?: "active" | "trialing" } = {}
) {
  // Wildcard fallback to prevent "Unable to connect"
  await page.route("**/v1/**", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [], status: { error: false } }),
    })
  })

  // Mock me & orgs to allow navigation to billing page
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

  // Mock billing subscription
  await page.route("**/v1/organizations/org-1/billing", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          plan: "growth",
          cycle: "monthly",
          status: opts.status ?? "active",
          currency: "USD",
          period_end: "2027-01-01T00:00:00Z",
          max_extendable_months: 24,
        },
        status: { error: false },
      }),
    })
  })

  // Mock plans catalog
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
            prices: { USD: { monthly: 900, yearly: 9000 } },
          },
          {
            id: "growth",
            name: "Growth",
            sort_order: 20,
            active: true,
            prices: { USD: { monthly: 2900, yearly: 29000 } },
          },
        ],
        status: { error: false },
      }),
    })
  })

  // Mock members
  await page.route("**/v1/organizations/org-1/members", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: "1", auth_sub: "e2e-auth-sub", role: "owner" },
          { id: "2", auth_sub: "user-2", role: "member" },
          { id: "3", auth_sub: "user-3", role: "member" },
        ],
        status: { error: false },
      }),
    })
  })
}

test("downgrade with no overage skips selection", async ({ page }) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page)
  await loginAsTestAccount(page)

  // Mock preview (no overage)
  await page.route(
    "**/v1/organizations/org-1/billing/preview*",
    async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            plan: "solo",
            cycle: "monthly",
            currency: "USD",
            plan_line_cents: 900,
            total_cents: 900,
          },
          status: { error: false },
        }),
      })
    }
  )

  await page.goto("/organization/org-1/billing")
  await page.getByRole("button", { name: /change plan/i }).click()

  // Select downgrade
  await page.getByLabel(/Solo/).click()
  await page.getByRole("button", { name: /continue/i }).click()

  // First Continue enters DowngradeWizard's own preview step; a second
  // Continue moves past it — no overage means straight to review.
  await expect(
    page.getByRole("heading", { name: /Downgrade to a lower plan/i })
  ).toBeVisible()
  await page.getByRole("button", { name: /continue/i }).click()

  // Verify review step is shown directly (no selection step) — active
  // subscriptions defer to renewal (PLAN-017), so this is the plain
  // scheduled-effective note, not the destructive/feature-loss copy that
  // only trialing (immediate-apply) downgrades show.
  await expect(
    page.getByRole("heading", { name: /Review Downgrade/i })
  ).toBeVisible()
  await expect(page.getByText(/no charge today/i)).toBeVisible()
  await expect(page.getByText(/takes effect at renewal/i)).toBeVisible()
})

// Trialing is the one status where a downgrade still applies immediately
// and can leave the subscription over its new plan's limits — so it's the
// only status where the selection/disclosure/candidates step still exists.
// An active subscription always defers to renewal (see the test above) and
// never shows this step regardless of overage; PLAN-017's overage warning
// for that case is a page-level OverageWarningCard, covered separately.
test("trialing downgrade with overage shows disclosure and candidates", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set"
  )
  await mockBillingAPIs(page, { status: "trialing" })
  await loginAsTestAccount(page)

  // Mock preview (WITH overage)
  await page.route(
    "**/v1/organizations/org-1/billing/preview*",
    async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: {
            plan: "solo",
            cycle: "monthly",
            currency: "USD",
            plan_line_cents: 900,
            total_cents: 900,
            overage: {
              members: {
                current: 3,
                allowed: 1,
                auto_select_removals: ["user-2", "user-3"],
              },
              storage: { current: 0, allowed: 1000, auto_select_removals: [] },
            },
          },
          status: { error: false },
        }),
      })
    }
  )

  const captured: {
    payload: { plan?: string; preferred_member_auth_subs?: string[] } | null
  } = { payload: null }
  await page.route(
    "**/v1/organizations/org-1/billing/downgrade",
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
  await page.getByRole("button", { name: /change plan/i }).click()

  // Select downgrade
  await page.getByLabel(/Solo/).click()
  await page.getByRole("button", { name: /continue/i }).click()

  // First Continue enters DowngradeWizard's own preview step; a second
  // Continue moves past it — overage means the selection step is next.
  await expect(
    page.getByRole("heading", { name: /Downgrade to a lower plan/i })
  ).toBeVisible()
  await page.getByRole("button", { name: /continue/i }).click()

  // Verify selection step and disclosure
  await expect(
    page.getByRole("heading", { name: /Select items to remove/i })
  ).toBeVisible()
  await expect(
    page.getByText(
      /anything you don't select will be chosen for you automatically/i
    )
  ).toBeVisible()

  // Proceed to review
  await page.getByRole("button", { name: /continue/i }).click()
  await expect(
    page.getByRole("heading", { name: /Review Downgrade/i })
  ).toBeVisible()

  // Submit downgrade
  await page.getByRole("button", { name: /confirm downgrade/i }).click()

  // Verify request sent empty arrays for manual picks, as we didn't interact
  expect(captured.payload).not.toBeNull()
  expect(captured.payload?.plan).toBe("solo")
  expect(captured.payload?.preferred_member_auth_subs).toEqual([])

  // Verify success screen distinguishing removed items
  await expect(
    page.getByRole("heading", { name: /Downgrade complete/i })
  ).toBeVisible()
})
