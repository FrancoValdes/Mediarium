import type { ReactNode } from 'react'
import { t, tx } from '../i18n'

// The wording for each third-party service, shared by Settings and the
// first-run wizard so both explain the same thing the same way.
export interface ServiceCopy {
  title: string
  required: boolean
  blurb: ReactNode
  without: ReactNode
  steps: ReactNode[]
  fieldLabel: string
  placeholder: string
}

const link = (href: string, text: string) => (
  <a href={href} target="_blank" rel="noreferrer">
    {text}
  </a>
)

export const TMDB_COPY: ServiceCopy = {
  title: t('TMDB (movie and show information)'),
  required: true,
  blurb: t("TMDB supplies every poster, title, description and episode list. It's free."),
  without: t("searching, Discover, adding movies and shows, the calendar and importing an existing library won't work."),
  steps: [
    tx('Create a free account at {link}.', { link: link('https://www.themoviedb.org/signup', 'themoviedb.org') }),
    tx('Open {link} and request an API key (choose "Developer" and describe it as personal use).', { link: link('https://www.themoviedb.org/settings/api', 'Settings > API') }),
    tx('Copy the {key} (the shorter v3 one) and paste it below.', { key: <strong>API Key</strong> }),
  ],
  fieldLabel: t('TMDB API key'),
  placeholder: t('v3 API key'),
}

export const OPENSUBTITLES_COPY: ServiceCopy = {
  title: t('OpenSubtitles (subtitles)'),
  required: false,
  blurb: t('OpenSubtitles is where subtitles come from. The API key lets Mediarium search it, and a free account raises your daily download limit.'),
  without: t('no subtitles are downloaded. Everything else works.'),
  steps: [
    tx('Create a free account at {link}.', { link: link('https://www.opensubtitles.com/en/newuser', 'opensubtitles.com') }),
    tx('Open {link} and create a consumer named "Mediarium".', { link: link('https://www.opensubtitles.com/en/consumers', t('your API consumers page')) }),
    tx('Copy its {key} and paste it below.', { key: <strong>{t('API key')}</strong> }),
  ],
  fieldLabel: t('OpenSubtitles API key'),
  placeholder: t('API key'),
}

export const SUBDL_COPY: ServiceCopy = {
  title: t('SubDL (more subtitles)'),
  required: false,
  blurb: t('SubDL is a second place to find subtitles. Mediarium searches it alongside OpenSubtitles and takes whichever fits your file best, and keeps going on SubDL when the OpenSubtitles daily limit is used up.'),
  without: t('subtitles only come from OpenSubtitles.'),
  steps: [
    tx('Create a free account at {link}.', { link: link('https://subdl.com', 'subdl.com') }),
    tx('Open your account panel and copy the {key}.', { key: <strong>{t('API key')}</strong> }),
    t('Paste it below.'),
  ],
  fieldLabel: t('SubDL API key'),
  placeholder: t('API key'),
}

export const TRAKT_COPY: ServiceCopy = {
  title: t('Trakt (list import)'),
  required: false,
  blurb: tx('Trakt is a free site where people publish lists ("Best sci-fi of the 2010s", "Oscar winners"). A Client ID lets you import any {public} list into Discover and add everything on it in one go.', {
    public: <em>{t('public')}</em>,
  }),
  without: t("you can't import Trakt lists on Discover."),
  steps: [
    tx("Sign in or sign up at {link} (it's free).", { link: link('https://trakt.tv', 'trakt.tv') }),
    tx('Open the {link} and choose {myApps}. If it asks, press {connect} once (Trakt only reads your public GitHub name).', {
      link: link('https://trakt.tv/oauth/applications', t('Trakt developer page')),
      myApps: <strong>My Apps</strong>,
      connect: <strong>Connect GitHub</strong>,
    }),
    tx('Press {create}, name it "Mediarium" and, if asked, set {redirect} to {uri}. Leave the rest empty and save.', {
      create: <strong>Create app</strong>,
      redirect: <strong>Redirect URI</strong>,
      uri: <code>urn:ietf:wg:oauth:2.0:oob</code>,
    }),
    tx('Copy the {clientId} (not the secret) and paste it below.', { clientId: <strong>Client ID</strong> }),
  ],
  fieldLabel: t('Trakt client ID'),
  placeholder: t('Client ID'),
}
