import { createPortal } from 'react-dom'
import { useEffect, useRef, useState } from 'react'
import { api, isAdmin } from '../api'
import { useAuth } from '../AuthContext'
import { currentLanguage, t } from '../i18n'
import { MESSAGE_MAX, PICTURE_MAX_BYTES, PICTURE_TYPES, TITLE_MAX, draftProblem, fitWithin, githubIssueURL, type ReportKind } from '../report'
import { formatSupportReport } from '../supportReport'
import { useFocusTrap } from '../useFocusTrap'
import { copyText } from './CopyBox'
import Icon from './Icon'
import Switch from './Switch'
import { useToast } from './Toast'

// Turns any picture into a JPEG of sensible size. Drawing it on a canvas and
// encoding it again also leaves behind anything hidden in the original file.
async function shrink(source: CanvasImageSource & { width: number; height: number }): Promise<string> {
  const size = fitWithin(source.width, source.height)
  if (!size.width) throw new Error(t("That picture couldn't be read."))
  const canvas = document.createElement('canvas')
  canvas.width = size.width
  canvas.height = size.height
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error(t("That picture couldn't be read."))
  ctx.fillStyle = '#ffffff'
  ctx.fillRect(0, 0, size.width, size.height)
  ctx.drawImage(source, 0, 0, size.width, size.height)
  return canvas.toDataURL('image/jpeg', 0.8)
}

async function pictureFromFile(file: File): Promise<string> {
  if (!PICTURE_TYPES.includes(file.type)) throw new Error(t('Pick a PNG, JPEG or WebP picture.'))
  if (file.size > PICTURE_MAX_BYTES) throw new Error(t('That picture is too large. Pick one under 15 MB.'))
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error(t("That picture couldn't be read."))
  })
  try {
    return await shrink(bitmap)
  } finally {
    bitmap.close()
  }
}

// Asks the browser to capture this tab (Chrome and Edge offer it straight
// away; other browsers ask which screen, window or tab) and takes one frame.
// beforeFrame runs once sharing has started, to hide anything that shouldn't
// be in the picture.
async function captureScreen(beforeFrame: () => Promise<void>): Promise<string> {
  const options = { video: { displaySurface: 'browser' }, audio: false, preferCurrentTab: true, selfBrowserSurface: 'include', surfaceSwitching: 'exclude' }
  const stream = await navigator.mediaDevices.getDisplayMedia(options as DisplayMediaStreamOptions)
  try {
    const video = document.createElement('video')
    video.srcObject = stream
    video.muted = true
    await video.play()
    await beforeFrame()
    // The browser's own "share this tab?" box takes a moment to fade away.
    await new Promise((r) => setTimeout(r, 400))
    const frame = { width: video.videoWidth, height: video.videoHeight }
    const canvas = document.createElement('canvas')
    canvas.width = frame.width
    canvas.height = frame.height
    canvas.getContext('2d')?.drawImage(video, 0, 0)
    return await shrink(canvas)
  } finally {
    stream.getTracks().forEach((track) => track.stop())
  }
}

const canCapture = () => typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getDisplayMedia

// "Report a problem or share an idea": one form, two ways to send it. Privately
// through mediarium.app (only the developer sees it), or as a public GitHub
// issue that opens with the form already filled in.
export default function ReportDialog({ onClose, initialKind = 'problem' }: { onClose: () => void; initialKind?: ReportKind }) {
  const { user } = useAuth()
  const admin = isAdmin(user)
  const toast = useToast()
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)

  const [kind, setKind] = useState<ReportKind>(initialKind)
  const [title, setTitle] = useState('')
  const [message, setMessage] = useState('')
  const [contact, setContact] = useState('')
  const [shot, setShot] = useState('')
  const [shotBusy, setShotBusy] = useState(false)
  // While picking, the window steps aside so the person can go to the part of
  // the app they want to show; a small bar takes the picture.
  const [picking, setPicking] = useState(false)
  const [hideBar, setHideBar] = useState(false)
  const [withReport, setWithReport] = useState(admin && initialKind === 'problem')
  const [report, setReport] = useState('')
  const [reportError, setReportError] = useState('')
  const [version, setVersion] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const [checked, setChecked] = useState(false)
  const [sentId, setSentId] = useState('')
  const [github, setGithub] = useState<{ withReport: boolean } | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || sending) return
      if (picking) setPicking(false)
      else onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, sending, picking])

  async function takeScreenshot() {
    await addPicture(() =>
      captureScreen(async () => {
        setHideBar(true)
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
      }),
    )
    setHideBar(false)
    setPicking(false)
  }

  useEffect(() => {
    api.version().then((v) => setVersion(v.version)).catch(() => undefined)
  }, [])

  // The support report is fetched once, the first time it's wanted.
  useEffect(() => {
    if (!admin || !withReport || report || reportError) return
    api
      .diagnostics()
      .then((d) => setReport(formatSupportReport(d)))
      .catch((e) => setReportError(e instanceof Error ? e.message : String(e)))
  }, [admin, withReport, report, reportError])

  const includeReport = admin && kind === 'problem' && withReport && !!report
  const problem = draftProblem({ title, message })
  const hint =
    problem === 'title'
      ? t('Give it a short title.')
      : problem === 'message'
        ? t("Describe what happened or what you'd like in a sentence or two.")
        : problem === 'title-long'
          ? t('The title is too long.')
          : problem === 'message-long'
            ? t('The description is too long.')
            : ''

  async function addPicture(work: () => Promise<string>) {
    setShotBusy(true)
    try {
      setShot(await work())
    } catch (e) {
      // Closing the browser's "choose what to share" window isn't an error.
      if (!(e instanceof DOMException && e.name === 'NotAllowedError')) toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setShotBusy(false)
    }
  }

  function onPaste(e: React.ClipboardEvent) {
    const file = [...e.clipboardData.files].find((f) => PICTURE_TYPES.includes(f.type))
    if (!file) return
    e.preventDefault()
    void addPicture(() => pictureFromFile(file))
  }

  async function sendPrivately() {
    setChecked(true)
    if (problem) return
    setSending(true)
    setError('')
    try {
      const res = await api.sendFeedback({
        kind,
        title: title.trim(),
        message: message.trim(),
        contact: contact.trim() || undefined,
        report: includeReport ? report : undefined,
        lang: currentLanguage(),
        screenshot: shot || undefined,
      })
      setSentId(res.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSending(false)
    }
  }

  async function openOnGitHub() {
    setChecked(true)
    if (problem) return
    // The support report is copied, not put in the link (see githubIssueURL).
    const withReport = kind === 'problem' && includeReport && report.trim() !== ''
    const { url } = githubIssueURL({ kind, title, message, version, report: withReport ? report : undefined })
    const copied = withReport && (await copyText(report))
    window.open(url, '_blank', 'noopener,noreferrer')
    setGithub({ withReport })
    if (copied) toast.success(t('Support report copied. Paste it into the Support report box.'))
  }

  async function copyReport() {
    if (await copyText(report)) toast.success(t('Support report copied. Paste it into the Support report box.'))
    else toast.error(t("Couldn't copy. Select the text and copy it by hand."))
  }

  // Browsers only put PNG pictures on the clipboard, so the JPEG is redrawn.
  async function copyPicture() {
    try {
      const png = await new Promise<Blob>((resolve, reject) => {
        const img = new Image()
        img.onload = () => {
          const c = document.createElement('canvas')
          c.width = img.width
          c.height = img.height
          c.getContext('2d')?.drawImage(img, 0, 0)
          c.toBlob((b) => (b ? resolve(b) : reject(new Error('png'))), 'image/png')
        }
        img.onerror = () => reject(new Error('img'))
        img.src = shot
      })
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': png })])
      toast.success(t('Screenshot copied. Paste it into the GitHub page.'))
    } catch {
      toast.error(t("Your browser didn't let Mediarium copy the picture. Download it and drag it into the GitHub page."))
    }
  }

  const closeButton = (
    <button className="icon-btn" onClick={onClose} aria-label={t('Close')} disabled={sending}>
      <Icon name="x" size={18} />
    </button>
  )

  if (sentId || github) {
    return createPortal(
      <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
        <div ref={box} tabIndex={-1} className="modal report-dialog report-done" role="dialog" aria-modal="true" aria-label={t('Report a problem or share an idea')}>
          <div className="share-head">
            <span className="share-ico">
              <Icon name="check" size={20} />
            </span>
            <div>
              <h2>{sentId ? t('Thanks, it was sent') : t('Finish it on GitHub')}</h2>
              <p>
                {sentId
                  ? t('It went straight to the developer and nobody else can see it. If you left a way to reach you, you may get a reply. Reference: {id}', { id: sentId })
                  : t('GitHub opened in a new tab with your text filled in. Sign in there if asked, check it and press Create.')}
              </p>
            </div>
            {closeButton}
          </div>
          {github && (github.withReport || shot) && (
            <div className="report-steps">
              {github.withReport && (
                <div className="report-step">
                  <p>{t('The support report is not put in the link. Paste it into the Support report box, and copy it again here if you need to.')}</p>
                  <button className="btn-with-icon" onClick={() => void copyReport()}>
                    <Icon name="copy" size={15} /> {t('Copy the support report')}
                  </button>
                </div>
              )}
              {shot && (
                <div className="report-step">
                  <p>{t('Add your screenshot: copy it and paste it into the GitHub page, or download it and drag it in.')}</p>
                  <div className="share-actions">
                    {typeof ClipboardItem !== 'undefined' && window.isSecureContext && (
                      <button className="btn-with-icon" onClick={() => void copyPicture()}>
                        <Icon name="copy" size={15} /> {t('Copy the screenshot')}
                      </button>
                    )}
                    <a className="btn-link" href={shot} download="mediarium-screenshot.jpg">
                      <Icon name="download" size={15} /> {t('Download the screenshot')}
                    </a>
                  </div>
                </div>
              )}
            </div>
          )}
          <div className="modal-foot">
            {github && <button onClick={() => setGithub(null)}>{t('Back to the form')}</button>}
            <button className="primary" onClick={onClose}>
              {t('Done')}
            </button>
          </div>
        </div>
      </div>,
      document.body,
    )
  }

  const shotBar = picking && !hideBar && (
    <div className="report-shot-bar" role="dialog" aria-label={t('Take a screenshot')}>
      <Icon name="camera" size={18} />
      <span>{t('Go to the part of Mediarium you want to show, then take the screenshot.')}</span>
      <button className="primary btn-with-icon" onClick={() => void takeScreenshot()} disabled={shotBusy}>
        <Icon name="camera" size={15} /> {shotBusy ? t('Taking it…') : t('Take screenshot')}
      </button>
      <button onClick={() => setPicking(false)} disabled={shotBusy}>
        {t('Back to the form')}
      </button>
    </div>
  )

  return createPortal(
    <>
    {shotBar}
    <div className="modal-backdrop" hidden={picking} onMouseDown={(e) => e.target === e.currentTarget && !sending && onClose()}>
      <div ref={box} tabIndex={-1} className="modal report-dialog" role="dialog" aria-modal="true" aria-label={t('Report a problem or share an idea')} onPaste={onPaste}>
        <div className="share-head">
          <span className="share-ico">
            <Icon name={kind === 'problem' ? 'bug' : 'sparkles'} size={20} />
          </span>
          <div>
            <h2>{t('Report a problem or share an idea')}</h2>
            <p>{t('Tell me what went wrong or what you would like Mediarium to do. Send it privately, or post it on GitHub where others can follow it.')}</p>
          </div>
          {closeButton}
        </div>

        <div className="report-body">
          <div className="report-form">
            <div className="seg" role="radiogroup" aria-label={t('What is it?')}>
              <button type="button" role="radio" aria-checked={kind === 'problem'} className={kind === 'problem' ? 'active' : ''} onClick={() => setKind('problem')}>
                <Icon name="bug" size={15} /> {t('A problem')}
              </button>
              <button type="button" role="radio" aria-checked={kind === 'idea'} className={kind === 'idea' ? 'active' : ''} onClick={() => setKind('idea')}>
                <Icon name="sparkles" size={15} /> {t('An idea')}
              </button>
            </div>
            <label>
              {t('Title')}
              <input
                value={title}
                maxLength={TITLE_MAX}
                onChange={(e) => setTitle(e.target.value)}
                placeholder={kind === 'problem' ? t('For example: Downloads stop at 99%') : t('For example: Sort the calendar by channel')}
              />
            </label>
            <label>
              {kind === 'problem' ? t('What happened?') : t('What would you like?')}
              <textarea
                rows={7}
                value={message}
                maxLength={MESSAGE_MAX}
                onChange={(e) => setMessage(e.target.value)}
                placeholder={
                  kind === 'problem'
                    ? t('What you did, what you expected, and what happened instead. Steps to make it happen again help a lot.')
                    : t('What you would like to do, and how Mediarium could help.')
                }
              />
            </label>
            <label>
              {t('Name to credit or a way to reach you (optional)')}
              <input value={contact} maxLength={200} onChange={(e) => setContact(e.target.value)} placeholder={t('For example: u/name on Reddit, @name on GitHub, or an email')} />
              <small className="report-help">{t('Only used to reply to you, or to thank you by name if your idea or report leads to a change.')}</small>
            </label>
            {checked && hint && <p className="error-text">{hint}</p>}
          </div>

          <div className="report-side">
            <div className="report-shot">
              <span className="report-label">{t('Screenshot (optional)')}</span>
              {shot ? (
                <figure>
                  <img src={shot} alt={t('The screenshot that will be sent')} />
                  <button className="icon-btn" onClick={() => setShot('')} aria-label={t('Remove the screenshot')} title={t('Remove the screenshot')}>
                    <Icon name="trash" size={16} />
                  </button>
                </figure>
              ) : (
                <div className="report-shot-empty">
                  <div className="share-actions">
                    {canCapture() && (
                      <button type="button" className="btn-with-icon" disabled={shotBusy} onClick={() => setPicking(true)}>
                        <Icon name="camera" size={15} /> {t('Take a screenshot')}
                      </button>
                    )}
                    <button type="button" className="btn-with-icon" disabled={shotBusy} onClick={() => fileInput.current?.click()}>
                      <Icon name="image" size={15} /> {t('Add a picture')}
                    </button>
                  </div>
                  <small className="report-help">{t('Or paste one here with Ctrl+V. It is shrunk and saved as a plain JPEG before it is sent.')}</small>
                </div>
              )}
              <input
                ref={fileInput}
                type="file"
                accept={PICTURE_TYPES.join(',')}
                hidden
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  e.target.value = ''
                  if (file) void addPicture(() => pictureFromFile(file))
                }}
              />
            </div>

            {admin && kind === 'problem' && (
              <div className="report-support">
                <Switch
                  checked={withReport}
                  onChange={setWithReport}
                  label={t('Include the support report')}
                  description={t('Your version, setup warnings, recent problems and the last 500 log lines. Passwords, keys and tokens are taken out.')}
                />
                {withReport && reportError && <p className="error-text">{reportError}</p>}
                {withReport && report && (
                  <details>
                    <summary>{t('See what is included')}</summary>
                    <textarea readOnly rows={8} value={report} className="report-preview" />
                  </details>
                )}
              </div>
            )}
          </div>
        </div>

        {error && <p className="error-text report-error">{error}</p>}

        <div className="modal-foot report-foot">
          <p className="share-note">
            {t('Sending privately goes only to the developer, through mediarium.app. GitHub issues are public: check the text and the report for anything private, like folder names, before you post.')}
          </p>
          <div className="share-actions">
            <button className="btn-with-icon" onClick={() => void openOnGitHub()} disabled={sending}>
              <Icon name="github" size={15} /> {t('Open on GitHub')}
            </button>
            <button className="primary btn-with-icon" onClick={() => void sendPrivately()} disabled={sending || shotBusy || (withReport && admin && kind === 'problem' && !report && !reportError)}>
              <Icon name="mail" size={15} /> {sending ? t('Sending…') : t('Send privately')}
            </button>
          </div>
        </div>
      </div>
    </div>
    </>,
    document.body,
  )
}
