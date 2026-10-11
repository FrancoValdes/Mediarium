import { useState } from 'react'
import type { Kind } from '../discoverSources'
import { useOwned } from '../useOwned'
import AddDialog, { type AddTarget } from './AddDialog'
import DiscoverRail from './DiscoverRail'
import { t } from '../i18n'

// Ideas for an empty library: one row of what is trending, each with the
// usual Add button, so a new install has something to pick straight away.
// onAdded lets the Library reload once the first title is in.
export default function StarterPicks({ kind, onAdded }: { kind: Kind; onAdded: () => void }) {
  const owned = useOwned()
  const [adding, setAdding] = useState<AddTarget | null>(null)
  return (
    <div className="starter-picks">
      <DiscoverRail
        title={kind === 'movie' ? t('Popular movies right now') : t('Popular shows right now')}
        hint={t('Not sure where to start? Add one of these, or look around Discover.')}
        source={{ kind, list: 'trending' }}
        kind={kind}
        owned={owned}
        onAdd={setAdding}
        rows={1}
        moreLabel={t('More on Discover')}
      />
      {adding && (
        <AddDialog
          target={adding}
          onClose={() => setAdding(null)}
          onAdded={() => {
            setAdding(null)
            owned.reload()
            onAdded()
          }}
        />
      )}
    </div>
  )
}
