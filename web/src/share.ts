// What people can post to tell others about Mediarium: ready-made text for
// each kind of place, links that open the site's own "new post" page, and
// the images in public/share. Kept apart from the dialog so it can be tested.
import { withBase } from './base.ts'
import { REPO_URL, WEBSITE_URL } from './docs.ts'
import { t } from './i18n.ts'

export interface SharePlace {
  id: 'reddit' | 'x' | 'bluesky' | 'facebook' | 'whatsapp' | 'instagram' | 'forum'
  name: string
  // The text to paste, links included.
  text: string
  // Opens the place with the text (or link) filled in, when it has such a page.
  open?: string
  // A title, for places that ask for one (Reddit).
  title?: string
  // Which picture suits it best.
  image: 'wide' | 'square' | 'story'
}

export const SHARE_IMAGES: { id: SharePlace['image']; file: string; size: string }[] = [
  { id: 'wide', file: withBase('/share/mediarium-wide.jpg'), size: '1200 × 630' },
  { id: 'square', file: withBase('/share/mediarium-square.jpg'), size: '1080 × 1080' },
  { id: 'story', file: withBase('/share/mediarium-story.jpg'), size: '1080 × 1920' },
]

// Communities where a post about a self-hosted media app usually fits.
export const COMMUNITIES = ['r/selfhosted', 'r/homelab', 'r/usenet', 'r/DataHoarder', 'r/jellyfin', 'r/PleX', 'r/unRAID', 'r/synology', 'Lemmy: selfhosted']

const enc = encodeURIComponent

export function sharePlaces(): SharePlace[] {
  const short = t('Mediarium: one self-hosted app for movies, shows, music and books, with Usenet and torrents built in. Free and open source.')
  const long = t(
    "I've been using Mediarium to look after my movies, shows, music and books. It does the jobs of Radarr, Sonarr, Lidarr, Prowlarr and SABnzbd in one container, with a torrent client and VPN support built in, and it's free and open source.",
  )
  const links = `${WEBSITE_URL}\n${REPO_URL}`
  const tags = '#selfhosted #homelab #plex #jellyfin #opensource'
  const redditTitle = t('Mediarium: one self-hosted app for movies, shows, music and books')
  return [
    { id: 'reddit', name: 'Reddit', title: redditTitle, text: `${long}\n\n${links}`, open: `https://www.reddit.com/submit?url=${enc(WEBSITE_URL)}&title=${enc(redditTitle)}`, image: 'wide' },
    { id: 'x', name: 'X', text: `${short} ${WEBSITE_URL}`, open: `https://x.com/intent/post?text=${enc(`${short} ${WEBSITE_URL}`)}`, image: 'wide' },
    { id: 'bluesky', name: 'Bluesky', text: `${short} ${WEBSITE_URL}`, open: `https://bsky.app/intent/compose?text=${enc(`${short} ${WEBSITE_URL}`)}`, image: 'wide' },
    { id: 'facebook', name: 'Facebook', text: `${long}\n\n${links}`, open: `https://www.facebook.com/sharer/sharer.php?u=${enc(WEBSITE_URL)}`, image: 'wide' },
    { id: 'whatsapp', name: 'WhatsApp', text: `${short} ${WEBSITE_URL}`, open: `https://wa.me/?text=${enc(`${short} ${WEBSITE_URL}`)}`, image: 'square' },
    { id: 'instagram', name: 'Instagram', text: `${short}\n\n${t('More at mediarium.app')}\n\n${tags}`, image: 'story' },
    { id: 'forum', name: t('Forums and Discord'), text: `${long}\n\n${links}`, image: 'wide' },
  ]
}
