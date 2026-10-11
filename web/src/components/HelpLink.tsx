import { Link } from 'react-router-dom'
import { helpPath } from '../guide/path.ts'
import { guideClick } from '../guide/popup.ts'
import { t } from '../i18n'
import InfoTip from './InfoTip'
import './InfoTip.css'

// A small question mark that leads to the right place in the user guide.
//
//   <HelpLink topic="downloads#torrent-port-58264" text={sentence} />
//   <HelpLink topic="quality" />
//
// `topic` is a guide topic id, optionally followed by "#anchor" of a heading in
// it (web/src/guide/topicDefs.ts lists the topics; the anchor is the heading
// text the way GitHub writes it). With `text`, pressing it shows that sentence
// and a "Learn more" link; without, it is a plain link to the guide. A test
// checks that every topic and anchor named in the code exists.
export default function HelpLink({ topic, text, label, className = '' }: { topic: string; text?: string; label?: string; className?: string }) {
  if (text) return <InfoTip glyph="?" text={text} learnMore={topic} label={label} className={className} />
  return (
    <Link to={helpPath(topic)} onClick={guideClick(topic)} className={`info-tip help${className ? ` ${className}` : ''}`} aria-label={label ?? t('Open the user guide')} title={label ?? t('Open the user guide')}>
      <span aria-hidden="true">?</span>
    </Link>
  )
}
