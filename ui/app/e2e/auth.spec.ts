import { test, expect } from "@playwright/test"
import { loginAsTestAccount } from "./helpers/login"

test.describe("Login page", () => {
  test("renders the sign-in form", async ({ page }) => {
    await page.goto("/login")
    await expect(
      page.getByRole("heading", { name: "Welcome back" })
    ).toBeVisible()
    await expect(page.getByLabel("Email")).toBeVisible()
    await expect(page.getByLabel("Password", { exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible()
  })

  test("password toggle reveals and hides the value", async ({ page }) => {
    await page.goto("/login")
    const password = page.getByLabel("Password", { exact: true })
    await password.fill("super-secret")
    await expect(password).toHaveAttribute("type", "password")

    await page.getByRole("button", { name: "Show password" }).click()
    await expect(password).toHaveAttribute("type", "text")

    await page.getByRole("button", { name: "Hide password" }).click()
    await expect(password).toHaveAttribute("type", "password")
  })

  test("shows an accessible error on invalid credentials", async ({ page }) => {
    await page.goto("/login")
    await page.getByLabel("Email").fill("nonexistent-e2e@example.com")
    await page
      .getByLabel("Password", { exact: true })
      .fill("definitely-wrong-password")
    await page.getByRole("button", { name: "Sign in" }).click()

    // role="alert" is the a11y fix under test here — a screen reader must
    // be told the sign-in failed, not just see red text appear silently.
    await expect(page.getByRole("alert")).toBeVisible({ timeout: 10_000 })
  })

  test("links to register", async ({ page }) => {
    await page.goto("/login")
    await page.getByRole("link", { name: "Create one" }).click()
    await expect(page).toHaveURL(/\/register$/)
  })
})

test.describe("Register page", () => {
  test("renders the sign-up form", async ({ page }) => {
    await page.goto("/register")
    await expect(
      page.getByRole("heading", { name: "Create an account" })
    ).toBeVisible()
    await expect(page.getByLabel("Work email")).toBeVisible()
    await expect(page.getByLabel("Password", { exact: true })).toBeVisible()
    await expect(
      page.getByRole("button", { name: "Create account" })
    ).toBeVisible()
  })

  test("password toggle reveals and hides the value", async ({ page }) => {
    await page.goto("/register")
    const password = page.getByLabel("Password", { exact: true })
    await password.fill("super-secret")
    await expect(password).toHaveAttribute("type", "password")

    await page.getByRole("button", { name: "Show password" }).click()
    await expect(password).toHaveAttribute("type", "text")
  })

  test("links to login", async ({ page }) => {
    await page.goto("/register")
    await page.getByRole("link", { name: "Sign in" }).click()
    await expect(page).toHaveURL(/\/login$/)
  })
})

test("real login with the shared test account leaves /login", async ({
  page,
}) => {
  test.skip(
    !process.env.TEST_ACCOUNT_EMAIL,
    "TEST_ACCOUNT_EMAIL not set — see .env.local"
  )
  // Exercises the real Supabase + backend round trip, not a mock — proves
  // the login page actually authenticates against live infra.
  await loginAsTestAccount(page)
  await expect(page).not.toHaveURL(/\/login$/)
})
