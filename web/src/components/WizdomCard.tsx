import { api } from '../api'
import { t } from '../i18n'
import { useAutosaveSetting } from '../useAutosave'
import Switch from './Switch'
import TestButton from './TestButton'

// Wizdom: a Hebrew subtitle site with a public API. No key or account, so the
// whole card is one switch and a test button. It sits with the other subtitle
// sources on the subtitle settings page.
export default function WizdomCard() {
  const sw = useAutosaveSetting<boolean>(
    (s) => s.subtitleWizdom ?? false,
    (v) => ({ subtitleWizdom: v }),
    (saved) => (saved ? t('Saved: Wizdom is on.') : t('Saved: Wizdom is off.')),
    false,
  )
  return (
    <section className="card grid-form">
      <h2>{t('Wizdom (Hebrew)')}</h2>
      <p style={{ color: 'var(--text-dim)', margin: 0 }}>
        {t('A Hebrew subtitle site. It needs no key or account. Mediarium only asks it when Hebrew is one of your subtitle languages.')}
      </p>
      <Switch checked={sw.value} onChange={(v) => void sw.change(v)} disabled={!sw.loaded || sw.saving} label={t('Look for Hebrew subtitles on Wizdom')} showState />
      <div>
        <TestButton run={() => api.testService({ service: 'wizdom' })} />
      </div>
    </section>
  )
}
