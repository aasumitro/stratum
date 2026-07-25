import { test, expect, type Page } from "@playwright/test"
import { loginAsTestAccount } from "./helpers/login"

// The onboarding step shown depends on the signed-in account's real profile
// state (has a name? has an organization?) — which we don't control and
// don't want to mutate on the shared TEST_ACCOUNT_OWNER_EMAIL account. So we log
// in for real (proving auth actually works) and then intercept just the
// two endpoints the /onboarding loader uses to decide the step, forcing a
// known state deterministically without touching backend data.

async function mockProfile(
  page: Page,
  profile: { full_name: string; onboarding_completed: boolean } | null
) {
  await page.route("**/v1/me", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    if (!profile) {
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({
          data: null,
          status: { request_id: "e2e", error: true, message: "not found" },
          pagination: {
            limit: 0,
            offset: 0,
            current_page: 1,
            total_pages: 1,
            total_items: 0,
          },
        }),
      })
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          id: "e2e-user",
          auth_sub: "e2e-auth-sub",
          email: "e2e@example.com",
          full_name: profile.full_name,
          avatar_url: "",
          preferences: { onboarding_completed: profile.onboarding_completed },
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
        status: { request_id: "e2e", error: false, message: "ok" },
        pagination: {
          limit: 0,
          offset: 0,
          current_page: 1,
          total_pages: 1,
          total_items: 1,
        },
      }),
    })
  })
}

async function mockNoOrganizations(page: Page) {
  await page.route("**/v1/organizations", async (route) => {
    if (route.request().method() !== "GET") return route.continue()
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [],
        status: { request_id: "e2e", error: false, message: "ok" },
        pagination: {
          limit: 0,
          offset: 0,
          current_page: 1,
          total_pages: 1,
          total_items: 0,
        },
      }),
    })
  })
}

test("profile step renders for an account with no profile yet", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set — see .env.local"
  )
  await loginAsTestAccount(page)
  await mockProfile(page, null)

  await page.goto("/onboarding")
  await expect(
    page.getByRole("heading", { name: "Complete your profile" })
  ).toBeVisible()
  await expect(page.getByLabel(/full name/i)).toBeVisible()
  await expect(page.getByLabel(/display name/i)).toBeVisible()
  await expect(page.getByRole("button", { name: "Continue" })).toBeVisible()
})

test("organization step renders for a profiled account with no org", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_OWNER_EMAIL,
    "TEST_ACCOUNT_OWNER_EMAIL not set — see .env.local"
  )
  await loginAsTestAccount(page)
  await mockProfile(page, {
    full_name: "E2E Test User",
    onboarding_completed: false,
  })
  await mockNoOrganizations(page)

  await page.goto("/onboarding")
  await expect(
    page.getByRole("heading", { name: "Set up your workspace" })
  ).toBeVisible()
  await expect(page.getByText("Create a new organization")).toBeVisible()
  await expect(page.getByText("Join with an invite code")).toBeVisible()
})
