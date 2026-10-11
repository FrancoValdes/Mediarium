import { useEffect, useState } from 'react'
import { OPEN_REPORT_EVENT, type ReportKind } from '../report'
import ReportDialog from './ReportDialog'

// Shows the report window when something asks for it (see openReport).
export default function ReportHost() {
  const [kind, setKind] = useState<ReportKind | null>(null)
  useEffect(() => {
    const open = (e: Event) => setKind((e as CustomEvent<ReportKind>).detail === 'idea' ? 'idea' : 'problem')
    window.addEventListener(OPEN_REPORT_EVENT, open)
    return () => window.removeEventListener(OPEN_REPORT_EVENT, open)
  }, [])
  return kind ? <ReportDialog initialKind={kind} onClose={() => setKind(null)} /> : null
}
