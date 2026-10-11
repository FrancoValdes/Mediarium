import { useEffect, useMemo, useState } from 'react'
import { t } from '../i18n'
import type { GuideData } from '../guide/build.ts'
import { indexGuide } from '../guide/guide.ts'
import { loadGuide } from '../guide/load'
import { Article } from '../pages/Guide'
import PageLoading from './PageLoading'
import '../guide/guide.css'

// The text of one guide topic, scrolled to its section, for the popup.
export default function GuidePopupBody({ topic }: { topic: string }) {
  const [data, setData] = useState<GuideData | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let live = true
    loadGuide().then(
      (d) => live && setData(d),
      () => live && setFailed(true),
    )
    return () => {
      live = false
    }
  }, [])
  const guide = useMemo(() => (data ? indexGuide(data) : null), [data])
  const [id, anchor = ''] = topic.split('#')
  const ti = guide?.byId.get(id) ?? null

  useEffect(() => {
    if (!ti || !anchor) return
    const timers = [0, 120, 350].map((ms) =>
      window.setTimeout(() => {
        const el = document.querySelector<HTMLElement>('.guide-pop-body [id="' + CSS.escape(decodeURIComponent(anchor)) + '"]')
        el?.scrollIntoView({ block: 'start' })
      }, ms),
    )
    return () => timers.forEach((x) => window.clearTimeout(x))
  }, [ti, anchor])

  if (failed) return <p className="g-note">{t('The guide could not be loaded.')}</p>
  if (!guide) return <PageLoading />
  if (guide.topics.length === 0) return <p className="g-note">{t('This copy of Mediarium was built without the guide.')}</p>
  if (!ti) return <p className="g-note">{t('There is no guide topic with that name. Pick one below.')}</p>
  return <Article ti={ti} guide={guide} jump={(sec) => document.querySelector<HTMLElement>('.guide-pop-body [id="' + CSS.escape(sec) + '"]')?.scrollIntoView({ block: 'start' })} popup />
}
