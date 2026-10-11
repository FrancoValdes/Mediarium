import { expect, test } from '@playwright/test'
import { randomBytes } from 'node:crypto'
import { mkdirSync, writeFileSync } from 'node:fs'
import { AUTH_FILE, CREDS_FILE, expectNoErrors, watchErrors } from './helpers'

// A fresh install: create the administrator, then walk through every step of
// setup, keeping the defaults and skipping what needs outside accounts,
// until the app itself shows.
test('first run: setup wizard to the dashboard', async ({ page }) => {
  const errors = watchErrors(page)
  const username = 'e2e-admin'
  const password = 'E2e-' + randomBytes(9).toString('base64url')

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Create your account' })).toBeVisible()
  await page.locator('input[autocomplete="username"]').fill(username)
  await page.locator('input[autocomplete="name"]').fill('Test Admin')
  const pw = page.locator('input[autocomplete="new-password"]')
  await pw.nth(0).fill(password)
  await pw.nth(1).fill(password)
  await page.getByLabel('I have read this and accept responsibility for how I use Mediarium.').check()
  await page.getByRole('button', { name: 'Continue', exact: true }).click()

  const nav = page.getByRole('navigation', { name: 'Main' })
  const next = page.getByRole('button', { name: /^(Continue anyway|Skip for now|Continue|Finish|Go to the dashboard|Open Mediarium|Start using Mediarium)/ }).first()
  for (let step = 0; step < 15; step++) {
    await Promise.race([nav.waitFor({ timeout: 15_000 }), next.waitFor({ timeout: 15_000 })]).catch(() => undefined)
    if (await nav.isVisible()) break
    // A question on top (such as "Continue without an indexer?") comes first.
    const dialog = page.locator('[role="dialog"], [role="alertdialog"]').first()
    if (await dialog.isVisible()) await dialog.getByRole('button', { name: /^(Continue anyway|Skip|Continue)/ }).first().click()
    else await next.click()
    await page.waitForTimeout(800)
  }
  await expect(nav).toBeVisible()

  // A new install asks about anonymous usage numbers, and nothing is sent
  // before the answer. Answer No, so the other tests are not in its way.
  const ask = page.getByRole('dialog', { name: 'Help Mediarium with anonymous numbers?' })
  await expect(ask).toBeVisible({ timeout: 20_000 })
  await ask.getByText('See exactly what is sent').click()
  await expect(ask.locator('pre')).toContainText('"schema": 1')
  await ask.getByRole('button', { name: 'No thanks' }).click()
  await expect(ask).toHaveCount(0)
  const answered = await page.request.get('/api/usage-stats')
  expect((await answered.json()).choice).toBe('no')

  mkdirSync('e2e/.auth', { recursive: true })
  writeFileSync(CREDS_FILE, JSON.stringify({ username, password }))
  await page.context().storageState({ path: AUTH_FILE })
  await expectNoErrors(errors)
})
