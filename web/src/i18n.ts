// Translations. The English text is the key: t('Save') looks the phrase up in
// the chosen language and falls back to the English when it isn't translated
// yet, so nothing ever shows blank. The language is picked per browser
// (Profile > Language, or the sign-in page) and starts from the browser's own
// language when it is one we have.
//
// Rules that keep the catalogues complete (tools/i18n-check.mjs finds every
// phrase by reading the code):
// - pass t(), tc(), tx() and plural() a plain string literal, never a variable;
// - put changing parts in {braces}: t('{name} was added', { name });
// - plural(n, '{n} movie', '{n} movies') picks the right form for the language.
import { Fragment, createElement, type ReactNode } from 'react'

export type Lang = 'en' | 'es' | 'de' | 'fr' | 'pt' | 'pt-PT'

export const LANGUAGES: { code: Lang; name: string }[] = [
  { code: 'en', name: 'English' },
  { code: 'es', name: 'Español' },
  { code: 'de', name: 'Deutsch' },
  { code: 'fr', name: 'Français' },
  { code: 'pt', name: 'Português (Brasil)' },
  { code: 'pt-PT', name: 'Português (Portugal)' },
]

const STORAGE_KEY = 'mediarium-lang'

let lang: Lang = 'en'
let table: Record<string, string> = {}

const isLang = (v: unknown): v is Lang => LANGUAGES.some((l) => l.code === v)

// The language this browser asked for: a stored choice first, then the first
// browser language we have a translation for, then English.
export function preferredLanguage(stored: string | null, browser: readonly string[]): Lang {
  if (isLang(stored)) return stored
  for (const b of browser) {
    // A regional language we have on its own (pt-PT) wins over its family.
    const exact = LANGUAGES.find((l) => l.code.toLowerCase() === b.toLowerCase())
    if (exact) return exact.code
    const code = b.toLowerCase().split('-')[0]
    if (isLang(code)) return code
  }
  return 'en'
}

function storedChoice(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

// Loads the chosen language before the app starts, so text set up when a
// file loads is translated too. English needs nothing loaded.
export async function loadLanguage(): Promise<void> {
  const want = preferredLanguage(storedChoice(), typeof navigator === 'undefined' ? [] : navigator.languages ?? [navigator.language])
  if (want !== 'en') {
    try {
      const mod =
        want === 'es'
          ? await import('./locales/es.json')
          : want === 'de'
            ? await import('./locales/de.json')
            : want === 'pt'
              ? await import('./locales/pt.json')
              : want === 'pt-PT'
                ? await import('./locales/pt-PT.json')
                : await import('./locales/fr.json')
      setCatalogue(want, mod.default as Record<string, string>)
    } catch {
      // A failed download leaves the app in English rather than broken.
      setCatalogue('en', {})
    }
  }
  if (typeof document !== 'undefined') document.documentElement.lang = lang
}

export function setCatalogue(code: Lang, entries: Record<string, string>) {
  lang = code
  table = entries
}

export function currentLanguage(): Lang {
  return lang
}

// Saves the choice and reloads, so every page comes back in the new language.
export function chooseLanguage(code: Lang) {
  try {
    localStorage.setItem(STORAGE_KEY, code)
  } catch {
    // private window: the choice lasts for this visit only
  }
  location.reload()
}

// The locale for dates and numbers: the browser's own for English (so a
// British or American reader keeps their date order), the language otherwise.
export function locale(): string | undefined {
  if (lang === 'en') return undefined
  return lang === 'pt' ? 'pt-BR' : lang
}

function fill(text: string, vars?: Record<string, string | number>): string {
  if (!vars) return text
  return text.replace(/\{(\w+)\}/g, (whole, name: string) => {
    if (!(name in vars)) return whole
    const v = vars[name]
    return typeof v === 'number' ? v.toLocaleString(locale()) : v
  })
}

export function t(text: string, vars?: Record<string, string | number>): string {
  return fill(table[text] || text, vars)
}

// Like t(), for a short word that means different things in different places
// ("Search" as a page title or as a button). The catalogue key is
// "context|English"; without its own entry it falls back to the plain phrase.
export function tc(context: string, text: string, vars?: Record<string, string | number>): string {
  return fill(table[`${context}|${text}`] || table[text] || text, vars)
}

// Like t(), for a sentence with links or bold words in it:
// tx('Press {save} when done', { save: <b>{t('Save')}</b> }).
export function tx(text: string, parts: Record<string, ReactNode>): ReactNode {
  const out: ReactNode[] = []
  const src = table[text] || text
  const re = /\{(\w+)\}/g
  let last = 0
  for (let m = re.exec(src); m; m = re.exec(src)) {
    if (!(m[1] in parts)) continue
    if (m.index > last) out.push(src.slice(last, m.index))
    const p = parts[m[1]]
    out.push(typeof p === 'number' ? p.toLocaleString(locale()) : p)
    last = m.index + m[0].length
  }
  if (last < src.length) out.push(src.slice(last))
  return createElement(Fragment, null, ...out)
}

// The singular or plural form for n. French counts 0 and 1 as singular,
// the other languages here only 1.
export function plural(n: number, one: string, other: string, vars?: Record<string, string | number>): string {
  const form = new Intl.PluralRules(lang).select(n) === 'one' ? one : other
  return t(form, { n, ...vars })
}
