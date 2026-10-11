import { useState } from 'react'
import { api, can, type SeriesType } from '../api'
import { useAuth } from '../AuthContext'
import { useToast } from './Toast'
import { t } from '../i18n'
import HelpLink from './HelpLink'

// On a show's page: how releases number its episodes. Anime releases count
// from the first episode ("Show - 105"); daily shows go by air date.
const LABEL: Record<SeriesType, string> = { standard: t('Seasons (S01E05)'), anime: t('Anime (episode 105)'), daily: t('Daily (by air date)') }

export default function SeriesTypePicker({ id, value, onChange }: { id: number; value: SeriesType; onChange: (t: SeriesType) => void }) {
  const toast = useToast()
  const me = useAuth().user
  const [busy, setBusy] = useState(false)
  async function change(type: SeriesType) {
    setBusy(true)
    try {
      await api.setSeriesType(id, type)
      onChange(type)
      toast.success(type === 'standard' ? t('Releases are matched by season and episode.') : type === 'anime' ? t('Releases are matched by episode number counted from the start, as anime is named.') : t('Releases are matched by the day the episode aired.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <label className="inline-field series-type" title={t("How release names number this show's episodes")}>
      <span className="has-tip">
        {t('Episode numbering')}
        <HelpLink topic="library#scene-and-anime-numbering" text={t('How release names count this show\'s episodes. Seasons is the usual S01E05; Anime counts from the first episode (Show - 105); Daily goes by the day the episode aired. Pick the one the releases of this show use.')} />
      </span>
      <select value={value} disabled={busy || !can(me, 'manage')} onChange={(e) => void change(e.target.value as SeriesType)}>
        {(Object.keys(LABEL) as SeriesType[]).map((type) => (
          <option key={type} value={type}>
            {LABEL[type]}
          </option>
        ))}
      </select>
    </label>
  )
}
