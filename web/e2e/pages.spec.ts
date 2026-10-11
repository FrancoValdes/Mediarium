import { expect, test } from '@playwright/test'
import { expectNoErrors, watchErrors } from './helpers'

// Every main page opens without an error on a new, empty install.
const PAGES: [string, RegExp][] = [
  ['/', /Dashboard/],
  ['/discover', /Discover/],
  ['/library', /Library/],
  ['/calendar', /Upcoming/],
  ['/wanted', /Upcoming/],
  ['/queue', /Activity/],
  ['/stats', /Statistics/],
  ['/settings/general', /General/],
  ['/settings/modules', /Media types/],
  ['/settings/media', /Library.*Folders/],
  ['/settings/naming', /File names and importing/],
  ['/settings/quality', /Quality/],
  ['/settings/automation', /Automatic searching/],
  ['/settings/indexers', /Indexers/],
  ['/settings/usenet', /Usenet/],
  ['/settings/torrents', /Torrents/],
  ['/settings/vpn', /VPN/],
  ['/settings/download-rules', /Download rules/],
  ['/settings/subtitles', /Subtitles/],
  ['/settings/metadata', /Movie and show info/],
  ['/settings/lists', /Import lists/],
  ['/settings/media-servers', /Media servers/],
  ['/settings/notifications', /Notifications/],
  ['/settings/other-apps', /Other apps/],
  ['/settings/profile', /Your profile/],
  ['/settings/security', /Security/],
  ['/settings/services', /Linked services/],
  ['/settings/accounts', /All accounts/],
  ['/settings/requests', /Requests and reports/],
  ['/settings/sign-in', /Ways to sign in/],
  ['/settings/backup', /Backup and restore/],
  ['/settings/system', /Server/],
  ['/settings/logs', /Logs and errors/],
  ['/settings/migrate', /Move from other apps/],
  ['/settings/about', /About and credits/],
  ['/help', /User guide/],
]

for (const [path, title] of PAGES) {
  test(`opens ${path}`, async ({ page }) => {
    const errors = watchErrors(page)
    await page.goto(path)
    await expect(page).toHaveTitle(title)
    await expect(page.getByText('Something went wrong')).toHaveCount(0)
    await expectNoErrors(errors)
  })
}

// Addresses that moved when the settings were split up still open.
const MOVED: [string, string][] = [
  ['/settings/downloads', '/settings/usenet'],
  ['/settings/language', '/settings/general'],
  ['/settings', '/settings/general'],
]
for (const [from, to] of MOVED) {
  test(`${from} opens ${to}`, async ({ page }) => {
    await page.goto(from)
    await expect(page).toHaveURL(new RegExp(`${to}$`))
  })
}

test('the Settings entry always lists its groups, and one group is open at a time', async ({ page }) => {
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: 'Main' })
  const library = nav.getByRole('button', { name: /^Library$/ }).last()
  const downloading = nav.getByRole('button', { name: /Downloading/ })
  await expect(library).toBeVisible()
  await library.click()
  await expect(library).toHaveAttribute('aria-expanded', 'true')
  await downloading.click()
  await expect(downloading).toHaveAttribute('aria-expanded', 'true')
  await expect(library).toHaveAttribute('aria-expanded', 'false')
})

test('an unknown address shows the not-found page', async ({ page }) => {
  await page.goto('/no-such-page')
  await expect(page.getByRole('heading', { level: 1 }).filter({ hasText: /exist|found/i })).toBeVisible()
})
