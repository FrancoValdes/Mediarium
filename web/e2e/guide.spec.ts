import { expect, test } from '@playwright/test'
import { expectNoErrors, watchErrors } from './helpers'

// The built-in user guide: it opens, search finds a topic, a link between two
// topics works, and the small "i" tips open and close the way they should.

test('the guide opens and lists its topics', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/help')
  await expect(page).toHaveTitle(/User guide/)
  await expect(page.getByRole('heading', { name: 'User guide', level: 1 })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Guide topics' }).getByRole('link', { name: 'Subtitles' })).toBeVisible()
  await expect(page.getByRole('link', { name: /Read the full documentation/ }).first()).toHaveAttribute('href', /github\.com/)
  await expectNoErrors(errors)
})

test('search finds a section and opens it at its heading', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/help')
  await page.getByRole('searchbox', { name: 'Search the guide' }).fill('torrent port')
  const hit = page.getByRole('link', { name: /Torrent port \(58264\)/ }).first()
  await expect(hit).toBeVisible()
  await hit.click()
  await expect(page).toHaveURL(/\/help\/downloads#torrent-port-58264$/)
  const heading = page.getByRole('heading', { name: /Torrent port \(58264\)/ })
  await expect(heading).toBeVisible()
  await expect(heading).toBeInViewport()
  // Nothing found says so.
  await page.goto('/help?q=qqqqzzzz')
  await expect(page.getByText(/Nothing in the guide matches/)).toBeVisible()
  await expectNoErrors(errors)
})

test('a link between two topics stays inside the guide', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/help/library')
  const link = page.locator('.g-article').getByRole('link', { name: 'books.md' }).first()
  await expect(link).toBeVisible()
  await link.click()
  await expect(page).toHaveURL(/\/help\/books/)
  await expect(page.getByRole('heading', { name: 'Ebooks and audiobooks', level: 1 })).toBeVisible()
  await expectNoErrors(errors)
})

test('another app language gets a note that the guide is in English', async ({ page }) => {
  await page.goto('/help')
  await expect(page.getByRole('note')).toHaveCount(0)
  await page.addInitScript(() => localStorage.setItem('mediarium-lang', 'de'))
  await page.goto('/help')
  // The note is translated too (German: Englisch), so only its presence is checked.
  await expect(page.getByRole('note')).toHaveCount(1)
})

test('a code block can be copied', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.goto('/help/install-docker')
  const copy = page.getByRole('button', { name: 'Copy' }).first()
  await expect(copy).toBeVisible()
  await copy.click()
  await expect(page.getByRole('button', { name: 'Copied' }).first()).toBeVisible()
})

test('the ? key, the header button and the profile menu all open the guide', async ({ page }) => {
  await page.goto('/library')
  await expect(page.getByRole('heading', { name: 'Library' }).first()).toBeVisible()
  await page.keyboard.press('?')
  await expect(page).toHaveURL(/\/help$/)
  // Typing a question mark in a box does not leave the page.
  await page.goto('/library')
  const box = page.getByRole('searchbox').first()
  await box.fill('')
  await box.press('?')
  await expect(page).toHaveURL(/\/library/)
  await page.getByRole('group', { name: 'Apps and links' }).getByRole('button', { name: 'User guide' }).first().click()
  await expect(page).toHaveURL(/\/help$/)
  await page.goto('/')
  await page.locator('.avatar').click()
  await page.getByRole('menuitem', { name: 'User guide' }).click()
  await expect(page).toHaveURL(/\/help$/)
})

test('the guide has no sideways scroll on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/help/downloads')
  await expect(page.getByRole('heading', { name: /Downloads: Usenet, torrents and VPN/, level: 1 })).toBeVisible()
  const wide = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1 || document.body.scrollWidth > window.innerWidth + 1)
  expect(wide).toBe(false)
  // The topic list is folded until asked for.
  const fold = page.locator('.g-fold-btn')
  await expect(fold).toBeVisible()
  await expect(page.getByRole('link', { name: 'Notifications' })).toBeHidden()
  await fold.click()
  await expect(page.getByRole('link', { name: 'Notifications' })).toBeVisible()
})

test('a tip opens on hover, stays when pressed and closes with Escape', async ({ page }) => {
  const errors = watchErrors(page)
  await page.goto('/queue')
  const tip = page.getByRole('button', { name: 'More information' }).first()
  await expect(tip).toBeVisible()
  const bubble = page.getByRole('tooltip')
  await expect(bubble).toHaveCount(0)

  // Hover shows it, moving away hides it.
  await tip.hover()
  await expect(bubble).toBeVisible()
  await expect(bubble).toContainText(/Downloads/)
  await page.mouse.move(5, 400)
  await expect(bubble).toHaveCount(0)

  // A press keeps it open even when the mouse leaves; Escape closes it.
  await tip.click()
  await expect(bubble).toBeVisible()
  await page.mouse.move(5, 400)
  await page.waitForTimeout(400)
  await expect(bubble).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(bubble).toHaveCount(0)

  // Keyboard: Enter on the focused button opens it; a press elsewhere closes it.
  await tip.focus()
  await page.keyboard.press('Enter')
  await expect(bubble).toBeVisible()
  await page.mouse.click(5, 400)
  await expect(bubble).toHaveCount(0)

  // The sentence is also the button's description, for screen readers.
  await expect(tip).toHaveAccessibleDescription(/Downloads running right now/)
  await expectNoErrors(errors)
})

test('a tip with a "Learn more" link opens the guide in a window over the page', async ({ page }) => {
  await page.goto('/queue?tab=bin')
  const help = page.getByRole('button', { name: 'Help' }).first()
  await expect(help).toBeVisible()
  await help.click()
  const group = page.getByRole('group', { name: 'Help' })
  await expect(group).toBeVisible()
  await group.getByRole('link', { name: 'Learn more' }).click()
  const popup = page.getByRole('dialog', { name: 'User guide' })
  await expect(popup).toBeVisible()
  await expect(page).toHaveURL(/\/queue\?tab=bin$/)
  await expect(popup.getByRole('link', { name: 'Open in the full guide' })).toHaveAttribute('href', /\/help\/downloads#the-recycle-bin$/)
  await page.keyboard.press('Escape')
  await expect(popup).toHaveCount(0)
})

test.describe('on a touch screen', () => {
  test.use({ hasTouch: true })
  test('a tap opens a tip and a tap elsewhere closes it', async ({ page }) => {
    await page.goto('/queue')
    const tip = page.getByRole('button', { name: 'More information' }).first()
    await tip.tap()
    await expect(page.getByRole('tooltip')).toBeVisible()
    await page.touchscreen.tap(5, 400)
    await expect(page.getByRole('tooltip')).toHaveCount(0)
  })
})

test('Escape closes a tip inside a window without closing the window', async ({ page }) => {
  // Demo data gives the Discover page titles to add without needing outside services.
  await page.addInitScript(() => localStorage.setItem('mediarium-demo', '1'))
  await page.goto('/discover')
  await page.getByRole('button', { name: /^Add$/ }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'More information' }).first().hover()
  await expect(page.getByRole('tooltip')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('tooltip')).toHaveCount(0)
  await expect(dialog).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
})
