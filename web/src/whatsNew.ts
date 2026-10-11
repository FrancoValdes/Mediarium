// "What's new" after an update: a few highlights per version, shown once in
// each browser after Mediarium moves to a newer version. Newest first. Keep
// each list short and in plain words; the full list is the changelog.
import { t } from './i18n.ts'

export interface ReleaseNotes {
  version: string // "3.0.0"
  items: string[]
}

export const WHATS_NEW: ReleaseNotes[] = [
  {
    version: '3.0.0',
    items: [
      t('A new look: a darker design with a colour and a shape for each kind of media, floating shapes behind the pages, and a layout that works on a phone.'),
      t('A music player built in: play an album or a whole artist, and it keeps going as you browse. It can open in a window of its own.'),
      t('Pick several audio languages in the order you like, including the language each film or show was made in (Settings > Library > Quality).'),
      t('The trailer plays behind a movie or show page, in place of the still picture. You can switch it off in Settings.'),
      t('Find the right number of Usenet connections for your provider with one button (Settings > Downloading > Usenet).'),
      t('Titles, plots and posters in about thirty languages, chosen apart from the screen language (Settings > Info, lists and subtitles).'),
      t('Send torrents to qBittorrent, RDT-Client or Decypharr, or to Real-Debrid or TorBox (Settings > Downloading > Torrents).'),
      t('Subtitle add-ons: paste the address of a Stremio add-on to search sites like subs.ro too (Settings > Info, lists and subtitles > Subtitles).'),
      t('Silo as a media server, with Watch in Silo buttons on every title.'),
      t('Skip bad releases is on in every profile: whole discs, 3D, upscaled copies and burned-in subtitles are left out.'),
      t('A calendar of what is coming out, and a clearer Activity page for what is downloading, waiting or failed.'),
      t('Keep a movie or show in two qualities, such as 4K next to 1080p (Settings > Library > Folders).'),
      t('Custom formats, with ready-made formats and profiles from the TRaSH Guides in a couple of clicks (Settings > Library > Quality).'),
      t('Two-step sign-in with a code from an authenticator app, offered when Mediarium can be reached from outside your home.'),
      t('Push notifications to your phone or computer, with no app needed (Settings > Connections > Notifications).'),
    ],
  },
]

export const SEEN_KEY = 'mediarium-seen-version'

// parts reads "3.0.0-beta.2" as [3, 0, 0]; a pre-release counts as its
// version, so betas of 3.0.0 show the 3.0.0 notes.
function parts(v: string): number[] | null {
  const m = /^v?(\d+)\.(\d+)\.(\d+)/.exec(v.trim())
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null
}

export function compareVersions(a: string, b: string): number {
  const x = parts(a)
  const y = parts(b)
  if (!x || !y) return 0
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] - y[i]
  return 0
}

// notesToShow lists the notes for every version newer than lastSeen, up to
// and including current. Nothing on a browser that has never seen
// Mediarium before (lastSeen null), or for a version that isn't a number.
export function notesToShow(lastSeen: string | null, current: string, notes: ReleaseNotes[] = WHATS_NEW): ReleaseNotes[] {
  if (!lastSeen || !parts(current) || !parts(lastSeen)) return []
  return notes.filter((n) => compareVersions(n.version, lastSeen) > 0 && compareVersions(n.version, current) <= 0)
}
