import { expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'

export const AUTH_FILE = 'e2e/.auth/admin.json'
export const CREDS_FILE = 'e2e/.auth/creds.json'

// The administrator the setup test created.
export function creds(): { username: string; password: string } {
  return JSON.parse(readFileSync(CREDS_FILE, 'utf8'))
}

// Collects uncaught errors in the page, so a test can fail on them.
export function watchErrors(page: Page): string[] {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  return errors
}

export async function expectNoErrors(errors: string[]) {
  expect(errors, errors.join('\n')).toEqual([])
}
