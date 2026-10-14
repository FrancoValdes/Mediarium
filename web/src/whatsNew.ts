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
    version: '3.1.0',
    items: [
      t('Five more screen languages: Italian, Dutch, Polish, Russian and Ukrainian. Error messages and notes from the server now come in your language too.'),
      t('Comics: .cbz, .cbr and .cb7 files open in the reader, one page or two, left to right or right to left.'),
      t('Plan your downloads: a speed schedule by day and hour, a weekly summary message, a Quiet for a while button and a switch to start newer releases first.'),
      t('The settings file now holds your indexers, Usenet servers, media servers and quality profiles too.'),
      t('A Backdrops view in the calendar, a page-turn animation in the ebook reader, and an Add-ons page for the programs you run next to Mediarium.'),
      t('A better installed app: shortcuts on the icon, a page that says when the server cannot be reached, and layouts for tablets and foldables.'),
      t('Send indexer traffic through the VPN, pick a book by hand when it is not found, and replace a movie with a smaller version.'),
      t('Movies and shows can have their own default quality profile, and each season has its own Find missing button.'),
    ],
  },
  {
    version: '3.0.4',
    items: [
      t('Transmission works as a download client (Settings > Downloading > Torrents).'),
      t('Keep your settings in a YAML file and apply it with the app config command (see the docs page Keep your settings in a file).'),
      t('In Activity, send a waiting download to the front, or tick several downloads and pause, resume, stop or move them together.'),
      t('Shows say if they have ended, episodes show their file size, release lists have an Age column, and the Library sorts without a leading The.'),
      t('Apps like Tdarr that change a file can now tell Mediarium to look again, and your media servers are asked to scan the folder.'),
    ],
  },
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
