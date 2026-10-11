import { expect, test } from '@playwright/test'
import { expectNoErrors, watchErrors } from './helpers'

test('dark by default; the button switches to light and it sticks', async ({ page }) => {
  await page.goto('/')
  const root = page.locator('html')
  await expect(root).toHaveAttribute('data-theme', 'dark')
  await page.getByRole('button', { name: 'Theme: dark' }).click()
  await expect(root).toHaveAttribute('data-theme', 'light')
  await page.reload()
  await expect(root).toHaveAttribute('data-theme', 'light')
  await page.getByRole('button', { name: 'Theme: light' }).click()
  await expect(root).toHaveAttribute('data-theme', 'dark')
})

test('switching to German and back', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/settings/general')
  await page.getByRole('combobox', { name: /language|sprache/i }).selectOption('de')
  await expect(page.locator('html')).toHaveAttribute('lang', 'de')
  await expect(page.getByRole('navigation', { name: 'Hauptmenü' }).getByText('Bibliothek').first()).toBeVisible()
  await page.getByRole('combobox', { name: /language|sprache/i }).selectOption('en')
  await expect(page.getByRole('navigation', { name: 'Main' }).getByText('Library').first()).toBeVisible()
  await expectNoErrors(errors)
})

test('the profile menu links to the project and opens the share dialog', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/')
  await page.locator('button.avatar').click()
  await expect(page.getByRole('menuitem', { name: 'Star on GitHub' })).toHaveAttribute('href', /github\.com\/rdborg\/Mediarium/)
  await page.getByRole('menuitem', { name: 'Share Mediarium' }).click()
  const dialog = page.getByRole('dialog', { name: 'Share Mediarium' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('tab', { name: 'Instagram' }).click()
  await expect(dialog.locator('textarea')).toContainText('#selfhosted')
  await expect(dialog.locator('img')).toHaveAttribute('src', /mediarium-story\.jpg/)
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expectNoErrors(errors)
})
