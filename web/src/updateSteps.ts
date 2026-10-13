// The plain-language pieces of the update card: the steps for each way of
// running Mediarium, and the short sentences about where the running program
// came from. No React in here so they can be tested.
import { t } from './i18n.ts'
import { compareVersions } from './whatsNew.ts'

export interface Steps {
  title: string
  steps: string[]
}

// Steps for updating the way the person installed Mediarium. Docker installs
// get Synology Container Manager, Unraid and plain Compose; the -full image gets
// one extra line about keeping its tag.
export function installSteps(install: { kind: 'docker' | 'native'; full: boolean }): { groups: Steps[]; notes: string[] } {
  if (install.kind === 'native') {
    return {
      groups: [
        {
          title: t('Update Mediarium'),
          steps: [
            t('Download a backup first (Settings, System).'),
            t('Download the new version from the release page and unpack it.'),
            t('Stop Mediarium, replace the program file with the new one, and start it again.'),
          ],
        },
      ],
      notes: [t('Your settings and library are kept. Mediarium updates its database when it starts.')],
    }
  }
  const tag = install.full ? 'latest-full' : 'latest'
  const notes = [t('Your settings and library are kept. Mediarium updates its database when it starts. Download a backup first (Settings, System).')]
  if (install.full) {
    notes.push(t('You use the image with the Cloudflare helper built in. Keep its tag ending in -full, for example {image}.', { image: `ghcr.io/rdborg/mediarium:${tag}` }))
  }
  return {
    groups: [
      {
        title: 'Synology Container Manager',
        steps: [
          t('Open Container Manager and go to Project.'),
          t('Select mediarium, then choose Action, Stop, and then Action, Clean. This removes the container, not your folders.'),
          t('Go to Image, select ghcr.io/rdborg/mediarium and delete it. If it says Update available, use that instead and skip the Build below.'),
          t('Go back to Project, select mediarium and choose Action, Build. Container Manager downloads the new version and starts it.'),
        ],
      },
      {
        title: 'Unraid',
        steps: [t('Open the Docker tab and click Check for Updates.'), t('When Mediarium says update ready, click it and choose Update.')],
      },
      {
        title: 'Docker Compose',
        steps: [t('In the folder with your compose file, run: docker compose pull'), t('Then run: docker compose up -d')],
      },
    ],
    notes,
  }
}

// Where the running program comes from, for the Updates card.
export function programSummary(s: { running: string; image: string; pushed: { version: string; running: boolean } | null }): string {
  if (!s.image) return t('You are running Mediarium {version}.', { version: s.running })
  if (s.pushed && s.pushed.running) {
    return t('You are running Mediarium {version}, installed on top of the Docker image, which has {image}.', { version: s.running, image: s.image })
  }
  if (s.pushed && compareVersions(s.pushed.version, s.image) < 0) {
    // Left over from before the image was updated: the start-up ignores it, so say that instead of "waiting".
    return t('You are running Mediarium {version}. An older installed update ({update}) is still stored, but it is ignored because the Docker image has {image}.', { version: s.running, update: s.pushed.version, image: s.image })
  }
  if (s.pushed) {
    return t('You are running Mediarium {version}. An installed update ({update}) is waiting for the next restart. The Docker image has {image}.', { version: s.running, update: s.pushed.version, image: s.image })
  }
  return t('You are running Mediarium {version}.', { version: s.running })
}

// The sentence about the last check.
export function checkedText(n: { enabled: boolean; checkedAt?: string; error?: string }, ago: (iso: string | undefined) => string): string {
  if (n.error) return n.checkedAt ? t('{error} The last good check was {ago}.', { error: n.error, ago: ago(n.checkedAt) }) : n.error
  if (n.checkedAt) return t('Checked {ago}.', { ago: ago(n.checkedAt) })
  return n.enabled ? t('Not checked yet.') : t('Checking is switched off. Press Check now to look.')
}

// How the page waits for Mediarium to come back after it restarts.
export interface WaitOptions {
  expectVersion?: string
  fetchVersion: () => Promise<string | null> // the running version, null while it does not answer
  sleep: (ms: number) => Promise<void>
  now: () => number
  minMs?: number // do not believe an answer before this: the old process may still be up
  maxMs?: number
  everyMs?: number
}

// Resolves 'back' once the app answers (with the expected version, if one is
// given), or 'timeout'.
export async function waitForApp(o: WaitOptions): Promise<'back' | 'timeout'> {
  const start = o.now()
  const minMs = o.minMs ?? 4000
  const maxMs = o.maxMs ?? 150000
  const every = o.everyMs ?? 2000
  for (;;) {
    await o.sleep(every)
    const elapsed = o.now() - start
    if (elapsed > maxMs) return 'timeout'
    const v = await o.fetchVersion()
    if (v !== null && elapsed >= minMs && (!o.expectVersion || v === o.expectVersion)) return 'back'
  }
}
