import { expect, test } from '@playwright/test'
import { expectNoErrors, watchErrors } from './helpers'

// What came with 3.1: more screen languages, the speed schedule, the weekly
// summary, "Quiet for a while", Add-ons and the calendar's Backdrops view.

test('the new screen languages load and a plural form reads right', async ({ page }) => {
  const errors = watchErrors(page)
  for (const [code, menu] of [
    ['it', 'Libreria'],
    ['nl', 'Bibliotheek'],
    ['pl', 'Biblioteka'],
    ['ru', 'Библиотека'],
    ['uk', 'Бібліотека'],
  ]) {
    await page.goto('/settings/general')
    await page.getByRole('combobox', { name: /language|sprache|lingua|taal|język|язык|мова/i }).selectOption(code)
    await expect(page.locator('html')).toHaveAttribute('lang', code)
    await expect(page.getByRole('navigation').first().getByText(menu).first()).toBeVisible()
  }
  await page.goto('/settings/general')
  await page.getByRole('combobox', { name: /язык|мова/i }).selectOption('en')
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expectNoErrors(errors)
})

test('the speed schedule: add a line, change it, remove it', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/settings/download-rules')
  await expect(page.getByText('Speed schedule').first()).toBeVisible()
  await page.getByRole('button', { name: 'Add a line' }).click()
  const limit = page.getByRole('combobox', { name: 'Limit' }).first()
  await expect(limit).toBeVisible()
  await limit.selectOption('5')
  await page.reload()
  await expect(page.getByRole('combobox', { name: 'Limit' }).first()).toHaveValue('5')
  await page.getByRole('button', { name: 'Remove' }).first().click()
  await page.reload()
  await expect(page.getByRole('combobox', { name: 'Limit' })).toHaveCount(0)
  await expectNoErrors(errors)
})

test('the weekly summary day and hour are saved', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/settings/notifications')
  await expect(page.getByText('Weekly summary').first()).toBeVisible()
  await page.getByRole('combobox', { name: 'Day' }).selectOption('3')
  await page.reload()
  await expect(page.getByRole('combobox', { name: 'Day' })).toHaveValue('3')
  await page.getByRole('combobox', { name: 'Day' }).selectOption('-1')
  await expectNoErrors(errors)
})

test('quiet for a while starts and ends', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/settings/automation')
  await page.getByRole('button', { name: '1 hour' }).click()
  await expect(page.getByText(/heavy background work waits until/i)).toBeVisible()
  await page.getByRole('button', { name: 'Back to normal' }).click()
  await expect(page.getByRole('button', { name: '1 hour' })).toBeVisible()
  await expectNoErrors(errors)
})

test('an add-on shows in the players menu and opens inside Mediarium', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/settings/addons')
  await page.getByRole('button', { name: 'Add an add-on' }).click()
  await page.getByRole('textbox', { name: 'Name' }).fill('Channel guide')
  await page.getByRole('textbox', { name: 'Address' }).fill('http://127.0.0.1:9/guide')
  await page.getByRole('combobox', { name: 'Opens' }).selectOption('frame')
  await page.getByRole('button', { name: 'Save' }).click()
  await page.reload()
  await expect(page.getByRole('textbox', { name: 'Name' })).toHaveValue('Channel guide')
  // The players menu (the play button at the bottom of the sidebar) lists it.
  await page.getByRole('button', { name: 'Players and media servers' }).first().click()
  await page.getByRole('menuitem', { name: /Channel guide/ }).click()
  await expect(page).toHaveURL(/\/apps\/[0-9a-f]+$/)
  await expect(page.locator('iframe[title="Channel guide"]')).toBeVisible()
  // Take it out again.
  await page.goto('/settings/addons')
  await page.getByRole('button', { name: 'Remove' }).first().click()
  await page.getByRole('button', { name: 'Save' }).click()
  await page.reload()
  await expect(page.getByRole('textbox', { name: 'Name' })).toHaveCount(0)
  await expectNoErrors(errors)
})

test('the calendar has a Backdrops view', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/calendar')
  await page.getByRole('button', { name: 'Backdrops' }).click()
  await expect(page.getByRole('button', { name: 'Backdrops' })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Month', exact: true }).click()
  await expectNoErrors(errors)
})

test('the server answers the new endpoints', async ({ request }) => {
  const quiet = await request.get('/api/automation/quiet')
  expect(quiet.ok()).toBeTruthy()
  expect(await quiet.json()).toEqual({ active: false })
  const apps = await request.get('/api/apps')
  expect(apps.ok()).toBeTruthy()
  const settings = await (await request.get('/api/settings')).json()
  expect(settings).toHaveProperty('speedSchedule')
  expect(settings).toHaveProperty('digestDay')
  expect(settings).toHaveProperty('newestFirst')
  const manifest = await (await request.get('/manifest.webmanifest')).json()
  expect(manifest.shortcuts.length).toBeGreaterThan(2)
  expect((await request.get('/offline.html')).ok()).toBeTruthy()
})
