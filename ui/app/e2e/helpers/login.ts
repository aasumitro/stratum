import type { Page } from "@playwright/test"

/**
 * Signs in with the shared dev test account (TEST_ACCOUNT_OWNER_EMAIL/PASSWORD
 * from the repo-root .env.local — the owner-role account; ADMIN/MEMBER
 * counterparts also exist for manual cross-role checks but aren't used by
 * these tests). This is a real Supabase + backend login, not a mock, so it
 * needs local infra and the API server running.
 */
export async function loginAsTestAccount(page: Page) {
  const email = process.env.TEST_ACCOUNT_OWNER_EMAIL
  const password = process.env.TEST_ACCOUNT_OWNER_PASSWORD
  if (!email || !password) {
    throw new Error(
      "TEST_ACCOUNT_OWNER_EMAIL/TEST_ACCOUNT_OWNER_PASSWORD are not set. Add them " +
        "to the repo-root .env.local (SEE: Docs)."
    )
  }

  await page.goto("/login")
  await page.getByLabel("Email").fill(email)
  await page.getByLabel("Password", { exact: true }).fill(password)
  await page.getByRole("button", { name: "Sign in" }).click()
  await page.waitForURL((url) => !url.pathname.startsWith("/login"), {
    timeout: 15_000,
  })
}
