import type { SubtitleProfile } from './api.ts'
import { t } from './i18n.ts'

// The choices a language can have, as one list to pick from. A choice is the
// kind of full subtitle (normal or SDH, normal only, SDH only, none) plus
// whether a forced track for the foreign-language parts comes with it.
export type ProfileChoice = 'either' | 'normal' | 'sdh' | 'forced' | 'either+forced' | 'normal+forced' | 'sdh+forced'

export const PROFILE_CHOICES: ProfileChoice[] = ['either', 'normal', 'sdh', 'forced', 'either+forced', 'normal+forced', 'sdh+forced']

export function choiceLabel(c: ProfileChoice): string {
  switch (c) {
    case 'either':
      return t('Normal or SDH')
    case 'normal':
      return t('Normal only')
    case 'sdh':
      return t('SDH only')
    case 'forced':
      return t('Forced only')
    case 'either+forced':
      return t('Normal or SDH, plus a forced track')
    case 'normal+forced':
      return t('Normal only, plus a forced track')
    case 'sdh+forced':
      return t('SDH only, plus a forced track')
  }
}

export function profileToChoice(p: SubtitleProfile | undefined): ProfileChoice {
  if (!p) return 'either'
  if (p.full === 'none') return 'forced'
  const base = p.full === 'normal' || p.full === 'sdh' ? p.full : 'either'
  return p.forced ? (`${base}+forced` as ProfileChoice) : base
}

export function choiceToProfile(c: ProfileChoice): SubtitleProfile {
  if (c === 'forced') return { full: 'none', forced: true }
  const forced = c.endsWith('+forced')
  const full = c.replace('+forced', '') as 'either' | 'normal' | 'sdh'
  return { full, forced }
}

// What is saved: only the languages with a choice other than the default.
export function profilesToSave(choices: Record<string, ProfileChoice>): Record<string, SubtitleProfile> {
  const out: Record<string, SubtitleProfile> = {}
  for (const [lang, c] of Object.entries(choices)) {
    if (c !== 'either') out[lang] = choiceToProfile(c)
  }
  return out
}

export function choicesFromSaved(saved: Record<string, SubtitleProfile> | undefined): Record<string, ProfileChoice> {
  const out: Record<string, ProfileChoice> = {}
  for (const [lang, p] of Object.entries(saved ?? {})) out[lang] = profileToChoice(p)
  return out
}

// A wanted subtitle as the server names it: "en" for a language's full
// subtitle and "en.forced" for its forced track.
export function slotName(key: string, languageName: (code: string) => string): string {
  return key.endsWith('.forced') ? t('{language} (forced)', { language: languageName(key.slice(0, -'.forced'.length)) }) : languageName(key)
}
