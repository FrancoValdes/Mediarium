// Checks Mediarium under a URL base (or at the root) in a real browser.
//
//   MEDIARIUM_URL=http://localhost:8315 URL_BASE=/mediarium node e2e/urlbase.check.mjs
//   MEDIARIUM_URL=http://localhost:8315 URL_BASE= node e2e/urlbase.check.mjs
//
// It needs a running Mediarium with an empty config folder, started with the
// same URL_BASE (or none). It creates the administrator itself, signs in, opens
// the main pages, and fails if any request the page made went outside the
// prefix. Set PW_CHANNEL (default "chrome") to use an installed browser. Set
// SHOTS to a folder to keep screenshots.
import { chromium } from '@playwright/test'
import { randomBytes } from 'node:crypto'
import { mkdirSync } from 'node:fs'

const origin = process.env.MEDIARIUM_URL ?? 'http://localhost:8264'
const prefix = (process.env.URL_BASE ?? '').replace(/\/+$/, '')
const shots = process.env.SHOTS
if (shots) mkdirSync(shots, { recursive: true })
const password = process.env.CHECK_PASSWORD ?? 'Check-' + randomBytes(9).toString('base64url') + '-pw'
const username = 'urlbase-admin'

const problems = []
const fail = (msg) => {
  problems.push(msg)
  console.log('FAIL  ' + msg)
}
const ok = (msg) => console.log('ok    ' + msg)
const check = (cond, msg) => (cond ? ok(msg) : fail(msg))

const browser = await chromium.launch({ channel: process.env.PW_CHANNEL || 'chrome' })
const context = await browser.newContext({ viewport: { width: 1400, height: 900 } })
const page = await context.newPage()

const requested = new Set()
const failedResponses = []
let signingOut = false
page.on('request', (r) => {
  const u = new URL(r.url())
  if (u.origin === origin) requested.add(u.pathname)
})
page.on('response', (r) => {
  const u = new URL(r.url())
  if (!signingOut && u.origin === origin && r.status() >= 400 && !(r.status() === 401 && u.pathname.endsWith('/api/auth/me'))) failedResponses.push(`${r.status()} ${u.pathname}`)
})
page.on('pageerror', (e) => fail('page error: ' + e.message))
page.on('console', (m) => {
  if (m.type() === 'error' && !/401|Failed to load resource/.test(m.text())) fail('console error: ' + m.text())
})

const shot = async (name) => {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png` })
}

try {
// 1. Set up the administrator through the API (as a script would), then sign in through the page.
const created = await context.request.post(`${origin}${prefix}/api/onboarding/admin`, {
  data: { username, password, firstName: 'Url', lastName: 'Base' },
})
check(created.status() === 201, `administrator created through ${prefix || '/'}api (status ${created.status()})`)
// The first-run wizard is not what is being checked here, so mark it done.
const done = await context.request.put(`${origin}${prefix}/api/settings`, { data: { onboardingDone: true } })
check(done.ok(), `setup marked done (status ${done.status()})`)
await context.clearCookies()

// 2. The bare prefix lands on the prefix with a slash; signed out we see the sign-in page.
await page.goto(`${origin}${prefix || '/'}`)
await page.waitForSelector('input[autocomplete="username"]')
check(new URL(page.url()).pathname === `${prefix}/`, `opening ${prefix || '/'} ends at ${prefix}/ (got ${new URL(page.url()).pathname})`)
const metaBase = await page.evaluate(() => document.querySelector('meta[name="mediarium-base"]')?.getAttribute('content') ?? '')
check(metaBase === prefix, `page base tag is "${prefix}" (got "${metaBase}")`)
await shot('1-sign-in')

// 3. A deep link while signed out shows the sign-in page, and after signing in we are on the same address.
const deep = `${prefix}/settings/notifications`
await page.goto(`${origin}${deep}`)
await page.waitForSelector('input[autocomplete="username"]')
await page.locator('input[autocomplete="username"]').fill(username)
await page.locator('input[autocomplete="current-password"]').fill(password)
await page.getByRole('button', { name: 'Sign in', exact: true }).click()
await page.getByRole('navigation', { name: 'Main' }).waitFor({ timeout: 20_000 })
check(new URL(page.url()).pathname === deep, `after signing in we are still at ${deep} (got ${new URL(page.url()).pathname})`)

// 4. The cookie is limited to the prefix.
const cookies = await context.cookies()
const session = cookies.find((c) => c.name === 'mediarium_session')
check(!!session && session.path === (prefix || '/'), `session cookie path is "${prefix || '/'}" (got "${session?.path}")`)

// 5. Reloading a deep link works, and so does moving around with the menu.
await page.reload()
await page.getByRole('navigation', { name: 'Main' }).waitFor({ timeout: 20_000 })
check(new URL(page.url()).pathname === deep, 'reloading a deep link keeps the address')
await page.getByRole('link', { name: 'Library', exact: true }).first().click()
await page.waitForURL(`${origin}${prefix}/library**`)
check(new URL(page.url()).pathname === `${prefix}/library`, 'the menu stays inside the prefix')
await shot('2-library')

// 6. Other pages open without errors.
for (const path of ['/settings/system', '/settings/other-apps', '/settings/backup', '/settings/logs', '/queue', '/wanted', '/calendar', '/discover']) {
  await page.goto(`${origin}${prefix}${path}`)
  await page.getByRole('navigation', { name: 'Main' }).waitFor({ timeout: 20_000 })
  await page.waitForTimeout(600)
  check(new URL(page.url()).pathname === `${prefix}${path}`, `${path} opens`)
}
await page.goto(`${origin}${prefix}/settings/other-apps`)
await page.getByRole('navigation', { name: 'Main' }).waitFor()
const otherApps = await page.locator('main').innerText().catch(() => '')
check(otherApps.includes(`${origin}${prefix}/radarr`), `the Other apps page shows ${origin}${prefix}/radarr`)
await shot('3-other-apps')

// 7. Files that browsers fetch by themselves.
for (const path of ['/manifest.webmanifest', '/push-sw.js', '/favicon.svg', '/fonts/HostGrotesk-Variable.ttf']) {
  const r = await context.request.get(`${origin}${prefix}${path}`)
  check(r.status() === 200, `${prefix}${path} answers 200 (got ${r.status()})`)
}
const manifest = await (await context.request.get(`${origin}${prefix}/manifest.webmanifest`)).json()
check(manifest.start_url === `${prefix}/` && manifest.scope === `${prefix}/` && manifest.icons[0].src.startsWith(`${prefix}/`), 'the manifest points inside the prefix')
const sw = await (await context.request.get(`${origin}${prefix}/push-sw.js`)).text()
check(sw.includes('self.registration.scope'), 'the push worker works out the prefix from its scope')

// 8. The service worker registers inside the prefix and takes its scope from it.
const reg = await page.evaluate(async () => {
  try {
    const link = document.querySelector('meta[name="mediarium-base"]')?.getAttribute('content') ?? ''
    const r = await navigator.serviceWorker.register(link + '/push-sw.js')
    return { scope: new URL(r.scope).pathname }
  } catch (e) {
    return { error: String(e) }
  }
})
check(reg.scope === `${prefix}/`, `push worker scope is ${prefix}/ (got ${JSON.stringify(reg)})`)

// 9. An API key works as a bearer token for the metrics, which are closed to anyone else.
const keyRes = await context.request.post(`${origin}${prefix}/api/auth/api-keys`, { data: { name: 'check' } })
const key = (await keyRes.json()).key
const anon = await browser.newContext()
const m1 = await anon.request.get(`${origin}${prefix}/metrics`)
check(m1.status() === 401, `metrics without a key are refused (got ${m1.status()})`)
const m2 = await anon.request.get(`${origin}${prefix}/metrics`, { headers: { Authorization: `Bearer ${key}` } })
const metricsText = await m2.text()
check(m2.status() === 200 && metricsText.includes('mediarium_up 1'), 'metrics with a bearer key answer')
const r1 = await anon.request.get(`${origin}${prefix}/radarr/api/v3/system/status`, { headers: { 'X-Api-Key': key } })
check(r1.status() === 200, `the Radarr-style API answers under the prefix (got ${r1.status()})`)
await anon.close()

// 10. Signing out returns to the sign-in page.
await page.goto(`${origin}${prefix}/`)
await page.getByRole('navigation', { name: 'Main' }).waitFor()
signingOut = true
await page.evaluate(async (p) => {
  await fetch(p + '/api/auth/logout', { method: 'POST' })
}, prefix)
await page.reload()
await page.waitForSelector('input[autocomplete="username"]')
check(true, 'after signing out the sign-in page shows again')

// 11. Every request this page made stayed inside the prefix.
const outside = [...requested].filter((p) => prefix !== '' && !(p === prefix || p.startsWith(prefix + '/')))
check(outside.length === 0, `all requests stayed under "${prefix || '/'}"` + (outside.length ? ': ' + outside.join(', ') : ''))
check(failedResponses.length === 0, 'no request failed' + (failedResponses.length ? ': ' + [...new Set(failedResponses)].join(', ') : ''))

} catch (e) {
  fail('stopped: ' + (e instanceof Error ? e.message.split(/\r?\n/)[0] : e))
  console.log('page text: ' + (await page.locator('body').innerText().catch(() => '')).slice(0, 500))
  await shot('stopped')
}

await browser.close()
if (problems.length) {
  console.log(`\n${problems.length} problem(s)`)
  process.exit(1)
}
console.log('\nall good')
