import { useEffect, useState } from 'react'
import { api } from '../api'
import { currentLanguage, t, type Lang } from '../i18n'
import Icon from './Icon'
import { useToast } from './Toast'

// The TMDB language that goes with each screen language, for the "use the same
// as the screen" shortcut.
const FOR_SCREEN: Record<Lang, string> = { en: 'en-US', es: 'es-ES', de: 'de-DE', fr: 'fr-FR', it: 'it-IT', nl: 'nl-NL', pl: 'pl-PL', pt: 'pt-BR', 'pt-PT': 'pt-PT', ru: 'ru-RU', uk: 'uk-UA' }

// The language titles, descriptions, genres and posters are read in. It is its
// own choice, apart from the screen language: someone can read Mediarium in
// English and still want film titles in Portuguese. Files and folders keep the
// English title either way.
export default function InfoLanguageCard({ headingLevel = 2 }: { headingLevel?: 2 | 3 }) {
  const toast = useToast()
  const [value, setValue] = useState('')
  const [options, setOptions] = useState<{ tag: string; name: string }[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        setValue(s.infoLanguage ?? 'en-US')
        setOptions(s.infoLanguages ?? [])
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  async function choose(tag: string) {
    setBusy(true)
    try {
      await api.putSettings({ infoLanguage: tag })
      setValue(tag)
      const name = options.find((o) => o.tag === tag)?.name ?? tag
      toast.success(t('Saved. Discover, search and title pages now use {language}.', { language: name }))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const suggested = FOR_SCREEN[currentLanguage()]
  const suggestedName = options.find((o) => o.tag === suggested)?.name
  const Heading = headingLevel === 2 ? 'h2' : 'h3'
  return (
    <section className="card info-language">
      <Heading>{t('Language for movie and show info')}</Heading>
      <p>{t('Titles, descriptions, genres and posters come from TMDB in this language. Where TMDB has no translation you see the English text.')}</p>
      <label className="field">
        {t('Show titles and descriptions in')}
        <select value={value} disabled={busy || options.length === 0} onChange={(e) => void choose(e.target.value)}>
          {options.map((o) => (
            <option key={o.tag} value={o.tag}>
              {o.name}
            </option>
          ))}
        </select>
      </label>
      {suggestedName && suggested !== value && (
        <button type="button" className="link-btn" disabled={busy} onClick={() => void choose(suggested)}>
          <Icon name="globe" size={14} /> {t('Use {language}, like the screen', { language: suggestedName })}
        </button>
      )}
      <p className="field-hint">{t('Your files and folders always use the English title, so changing this never renames anything.')}</p>
      {error && <p className="error-text">{error}</p>}
    </section>
  )
}
