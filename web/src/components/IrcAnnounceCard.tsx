import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type IrcAnnounceList, type IrcConnection, type IrcLogRow, type IrcPreset, type IrcTemplate, type IrcTestResult } from '../api'
import { formatBytes, timeAgo } from '../format'
import { channelsText, neededKeys, parseChannels, parseList, stateOf } from '../ircAnnounce'
import { t, tx, st } from '../i18n'
import { useLive } from '../useLive'
import { FieldError, FormProblem, useValidation } from '../useValidation'
import { firstError, hostOrIP, port as portCheck, required, url as urlCheck } from '../validate'
import { useConfirm } from './ConfirmProvider'
import Icon from './Icon'
import Switch from './Switch'
import { useToast } from './Toast'
import '../irc.css'
import TipLabel from './TipLabel'

const DECISIONS: Record<IrcLogRow['decision'], () => string> = {
  grabbed: () => t('Grabbed'),
  'would-grab': () => t('Would grab'),
  skipped: () => t('Skipped'),
  duplicate: () => t('Seen before'),
  ignored: () => t('Not read'),
  failed: () => t('Failed'),
}

const STATES = {
  off: () => t('Off'),
  problem: () => t("Can't start"),
  listening: () => t('Listening'),
  joining: () => t('Connected, joining the channel'),
  notInChannel: () => t('Connected, but not in the channel'),
  cannotConnect: () => t("Can't connect"),
  connecting: () => t('Connecting'),
  notRunning: () => t('Not running'),
}

// IRC announce: private and public trackers post each new release in an IRC
// channel seconds after it is uploaded. Mediarium can listen there and grab a
// release you want the moment it appears, instead of waiting for the next RSS
// check. Off until a connection is added and switched on.
export default function IrcAnnounceCard() {
  const toast = useToast()
  const confirm = useConfirm()
  const [data, setData] = useState<IrcAnnounceList | null>(null)
  const [editing, setEditing] = useState<'new' | number | null>(null)
  const [logOpen, setLogOpen] = useState<number | null>(null)

  const reload = useCallback(() => {
    api.listIrcAnnounce().then(setData).catch(() => setData((cur) => cur ?? { connections: [], presets: [] }))
  }, [])
  useEffect(reload, [reload])
  useLive(reload, 5000)

  async function toggle(c: IrcConnection, on: boolean) {
    try {
      await api.setIrcConnectionEnabled(c.id, on)
      toast.success(on ? t('{name} is on. It connects now.', { name: c.name }) : t('{name} is off.', { name: c.name }))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function remove(c: IrcConnection) {
    if (!(await confirm({ title: t('Remove {name}?', { name: c.name }), body: <p>{t('Mediarium disconnects and forgets the saved passwords and keys.')}</p>, confirmLabel: t('Remove'), danger: true }))) return
    try {
      await api.deleteIrcConnection(c.id)
      toast.success(t('{name} removed.', { name: c.name }))
      if (editing === c.id) setEditing(null)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  const connections = data?.connections ?? []
  const editingConn = typeof editing === 'number' ? connections.find((c) => c.id === editing) : undefined

  return (
    <fieldset className="group torrent irc-card">
      <legend>
        <Icon name="chat" size={14} /> <TipLabel help="indexers#grabbing-from-irc-announcements">{t('IRC announce')}</TipLabel>
      </legend>
      <p>
        {t('Trackers post every new release in an IRC channel within seconds of the upload. Mediarium can listen there and download a release you want right away, instead of waiting for the next check. It only ever downloads titles that are in your library and wanted, with the same quality rules as everywhere else. Nothing runs until you add a connection and switch it on.')}
      </p>
      {data && connections.length === 0 && editing === null && <p>{t('No connections yet.')}</p>}
      {connections.length > 0 && (
        <div className="irc-list">
          {connections.map((c) => (
            <ConnectionCard key={c.id} c={c} logOpen={logOpen === c.id} onToggle={(on) => void toggle(c, on)} onEdit={() => setEditing(c.id)} onRemove={() => void remove(c)} onLog={() => setLogOpen(logOpen === c.id ? null : c.id)} />
          ))}
        </div>
      )}
      {editing === null && data && (
        <button onClick={() => setEditing('new')} disabled={connections.length >= 10}>
          <Icon name="plus" size={14} /> {t('Add a connection')}
        </button>
      )}
      {data && editing !== null && (
        <Editor
          key={editing === 'new' ? 'new' : `edit-${editing}`}
          existing={editingConn}
          presets={data.presets}
          onCancel={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            reload()
          }}
        />
      )}
    </fieldset>
  )
}

function ConnectionCard({ c, logOpen, onToggle, onEdit, onRemove, onLog }: { c: IrcConnection; logOpen: boolean; onToggle: (on: boolean) => void; onEdit: () => void; onRemove: () => void; onLog: () => void }) {
  const st = stateOf(c)
  const channelProblem = c.status.channels.find((ch) => ch.error)
  return (
    <div className={`irc-conn${c.enabled ? '' : ' is-off'}`}>
      <div className="irc-conn-top">
        <strong>{c.name}</strong>
        <span className={`irc-state ${st.tone}`}>
          <span className="irc-dot" aria-hidden="true" />
          {STATES[st.key]()}
        </span>
        <Switch checked={c.enabled} onChange={onToggle} label={c.enabled ? t('On') : t('Off')} />
      </div>
      <p className="irc-meta">
        {c.server}:{c.port} · {c.channels.map((ch) => ch.name).join(', ')} · {c.nick}
      </p>
      {c.enabled && c.status.state === 'joined' && (
        <p className="irc-meta">
          {c.status.lastAnnounce ? t('Last announcement {when}.', { when: timeAgo(c.status.lastAnnounce) }) : t('No announcement yet.')} {t('{heard} heard and {grabbed} downloaded since it connected.', { heard: c.status.heard, grabbed: c.status.grabbed })}
        </p>
      )}
      {c.enabled && c.status.problem && <p className="irc-problem">{c.status.problem}</p>}
      {c.enabled && c.status.state !== 'joined' && c.status.lastError && <p className="irc-problem">{c.status.lastError}</p>}
      {c.enabled && channelProblem && <p className="irc-problem">{t('{channel}: {error}', { channel: channelProblem.name, error: channelProblem.error ?? '' })}</p>}
      <div className="irc-actions">
        <button className="btn-sm" onClick={onEdit}>
          {t('Edit')}
        </button>
        <button className="btn-sm" onClick={onLog} aria-expanded={logOpen}>
          {logOpen ? t('Hide the log') : t('Show the log')}
        </button>
        <button className="btn-sm danger" onClick={onRemove}>
          {t('Remove')}
        </button>
      </div>
      {logOpen && <Log id={c.id} />}
    </div>
  )
}

// The last 50 announcements and what became of each.
function Log({ id }: { id: number }) {
  const [rows, setRows] = useState<IrcLogRow[] | null>(null)
  const load = useCallback(() => {
    api.ircAnnounceLog(id).then(setRows).catch(() => setRows((cur) => cur ?? []))
  }, [id])
  useEffect(load, [load])
  useLive(load, 4000)
  if (!rows) return null
  if (rows.length === 0) return <p className="irc-meta">{t('Nothing announced since it connected.')}</p>
  return (
    <div className="irc-log">
      <table>
        <thead>
          <tr>
            <th>{t('When')}</th>
            <th>{t('Release')}</th>
            <th>{t('What happened')}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={`${r.at}-${i}`}>
              <td className="when">{timeAgo(r.at)}</td>
              <td className="rel" title={r.announcer ? t('From {nick}', { nick: r.announcer }) : undefined}>
                {r.release}
                {r.sizeBytes > 0 && <small> · {formatBytes(r.sizeBytes)}</small>}
              </td>
              <td>
                <span className={`irc-decision ${r.decision}`}>{DECISIONS[r.decision]?.() ?? r.decision}</span> {r.item && <strong>{r.item}. </strong>}
                {st(r.reason)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const CUSTOM: IrcTemplate = { multi: false, patterns: [''], sizeUnits: 'binary', downloadUrl: '' }

function Editor({ existing, presets, onCancel, onSaved }: { existing?: IrcConnection; presets: IrcPreset[]; onCancel: () => void; onSaved: () => void }) {
  const toast = useToast()
  const [preset, setPreset] = useState(existing?.preset ?? '')
  const [name, setName] = useState(existing?.name ?? '')
  const [server, setServer] = useState(existing?.server ?? '')
  const [port, setPort] = useState(existing ? String(existing.port) : '6697')
  const [tls, setTls] = useState(existing?.tls ?? true)
  const [insecureTls, setInsecureTls] = useState(existing?.insecureTls ?? false)
  const [nick, setNick] = useState(existing?.nick ?? '')
  const [auth, setAuth] = useState<string>(existing?.auth ?? 'nickserv')
  const [authAccount, setAuthAccount] = useState(existing?.authAccount ?? '')
  const [authPassword, setAuthPassword] = useState('')
  const [invite, setInvite] = useState('')
  const [clearInvite, setClearInvite] = useState(false)
  const [channels, setChannels] = useState(existing ? channelsText(existing) : '')
  const [announcers, setAnnouncers] = useState(existing?.announcers.join(', ') ?? '')
  const [trackerUrl, setTrackerUrl] = useState(existing?.trackerUrl ?? '')
  const [multi, setMulti] = useState(existing?.template.multi ?? false)
  const [patterns, setPatterns] = useState((existing?.template.patterns ?? CUSTOM.patterns).join('\n'))
  const [sizeUnits, setSizeUnits] = useState(existing?.template.sizeUnits || 'binary')
  const [downloadUrl, setDownloadUrl] = useState(existing?.template.downloadUrl ?? '')
  const [keys, setKeys] = useState<Record<string, string>>({})
  const [freeleechOnly, setFreeleechOnly] = useState(existing?.freeleechOnly ?? false)
  const [maxPerHour, setMaxPerHour] = useState(String(existing?.maxPerHour ?? 5))
  const [maxPerDay, setMaxPerDay] = useState(String(existing?.maxPerDay ?? 20))
  const [saving, setSaving] = useState(false)
  const [testLines, setTestLines] = useState('')
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<IrcTestResult | null>(null)
  const [testError, setTestError] = useState('')

  const presetInfo = presets.find((p) => p.id === preset)
  const template: IrcTemplate = useMemo(() => ({ multi, patterns: patterns.split(/\r?\n/).map((p) => p.trim()).filter(Boolean), sizeUnits, downloadUrl: downloadUrl.trim() }), [multi, patterns, sizeUnits, downloadUrl])
  const keyNames = useMemo(() => neededKeys(template), [template])
  const channelList = parseChannels(channels)
  const announcerList = parseList(announcers)
  const passwordSaved = !!existing?.hasPassword

  const v = useValidation({
    name: firstError(required(name, t('Give this connection a name.'))),
    server: firstError(required(server, t('Add the IRC server, for example {example}.', { example: 'irc.example.org' })), hostOrIP(server, 'irc.example.org')),
    port: portCheck(port),
    nick: required(nick, t('Add the nick the bot signs in with, for example {example}.', { example: 'yourname_bot' })),
    password: auth !== 'none' && !authPassword && !passwordSaved ? t('Enter the password for this sign-in method.') : null,
    channels: channelList.length === 0 ? t('Add the announce channel, for example {example}.', { example: '#announce' }) : null,
    announcers: announcerList.length === 0 ? t('Add the nick of the bot that posts the announcements.') : null,
    trackerUrl: firstError(required(trackerUrl, t("Add the tracker's web address, for example {example}.", { example: 'https://tracker.example.org' })), urlCheck(trackerUrl, { requireScheme: true, example: 'https://tracker.example.org' })),
    patterns: template.patterns.length === 0 ? t('Add at least one pattern.') : null,
    downloadUrl: required(downloadUrl, t('Add the download address, for example {example}.', { example: '/download.php?id={{ .torrentId }}&passkey={{ .passkey }}' })),
    keys: keyNames.find((k) => !keys[k] && !existing?.keys.find((x) => x.name === k)?.hasValue) ? t('Fill in the keys the download address uses.') : null,
  })

  function choosePreset(id: string) {
    setPreset(id)
    setResult(null)
    const p = presets.find((x) => x.id === id)
    if (!p) return
    setMulti(p.template.multi)
    setPatterns(p.template.patterns.join('\n'))
    setSizeUnits(p.template.sizeUnits || 'binary')
    setDownloadUrl(p.template.downloadUrl)
    setAuth(p.auth || 'nickserv')
    setKeys({})
  }

  function body() {
    return {
      name: name.trim(),
      preset: preset || 'custom',
      server: server.trim(),
      port: Number(port) || undefined,
      tls,
      insecureTls: tls && insecureTls,
      nick: nick.trim(),
      auth,
      authAccount: auth === 'none' ? '' : authAccount.trim(),
      ...(authPassword ? { authPassword } : {}),
      ...(invite.trim() ? { invite: invite.trim() } : {}),
      ...(clearInvite && !invite.trim() ? { clearInvite: true } : {}),
      channels: channelList,
      announcers: announcerList,
      trackerUrl: trackerUrl.trim(),
      template,
      keys: keyNames.map((k) => ({ name: k, value: keys[k] ?? '' })),
      freeleechOnly,
      maxPerHour: Number(maxPerHour) || 5,
      maxPerDay: Number(maxPerDay) || 20,
    }
  }

  async function save() {
    if (!v.attempt()) return
    setSaving(true)
    try {
      if (existing) await api.updateIrcConnection(existing.id, body())
      else await api.createIrcConnection({ ...body(), enabled: false })
      toast.success(existing ? t('{name} saved.', { name: name.trim() }) : t('{name} saved. It is off: switch it on when you are ready.', { name: name.trim() }))
      onSaved()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function runTest() {
    setTesting(true)
    setResult(null)
    setTestError('')
    try {
      setResult(await api.testIrcAnnounce({ id: existing?.id, template, trackerUrl: trackerUrl.trim(), freeleechOnly, lines: testLines.split(/\r?\n/).filter((l) => l.trim()) }))
    } catch (e) {
      setTestError(e instanceof Error ? e.message : String(e))
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="irc-editor">
      <h3>{existing ? t('Edit {name}', { name: existing.name }) : t('Add a connection')}</h3>
      <label>
        {t('Announcement style')}
        <select value={preset} onChange={(e) => choosePreset(e.target.value)}>
          <option value="">{t('Choose one…')}</option>
          {presets.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
          <option value="custom">{t('Write my own')}</option>
        </select>
        {presetInfo && <small style={{ color: 'var(--text-dim)' }}>{presetInfo.description}</small>}
        {!presetInfo && <small style={{ color: 'var(--text-dim)' }}>{t("Pick the one that looks like the lines your tracker's bot posts. You can change everything after.")}</small>}
      </label>
      <div className="irc-editor-cols">
        <section>
          <h3>{t('The IRC network')}</h3>
          <label>
            {t('Name')}
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('My tracker')} {...v.bind('name', name, setName)} />
            <FieldError v={v} name="name" />
          </label>
          <div className="irc-row2">
            <label>
              {t('IRC server')}
              <input value={server} onChange={(e) => setServer(e.target.value)} placeholder="irc.example.org" spellCheck={false} {...v.bind('server', server, setServer)} />
              <FieldError v={v} name="server" />
            </label>
            <label>
              {t('Port')}
              <input inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} {...v.bind('port', port, setPort)} />
              <FieldError v={v} name="port" />
            </label>
          </div>
          <label className="check-hint">
            <input type="checkbox" checked={tls} onChange={(e) => { setTls(e.target.checked); if (e.target.checked && port === '6667') setPort('6697'); if (!e.target.checked && port === '6697') setPort('6667') }} />
            <span>{t('Use a secure connection (TLS)')}</span>
          </label>
          {tls && (
            <label className="check-hint">
              <input type="checkbox" checked={insecureTls} onChange={(e) => setInsecureTls(e.target.checked)} />
              <span>
                {t("Accept a certificate that can't be checked")}
                <small>{t('Only for a server with a self-signed or expired certificate. Your password could be read by someone in between.')}</small>
              </span>
            </label>
          )}
          <label>
            {t('Nick')}
            <input value={nick} onChange={(e) => setNick(e.target.value)} placeholder="yourname_bot" spellCheck={false} autoComplete="off" {...v.bind('nick', nick, setNick)} />
            <FieldError v={v} name="nick" />
            <small style={{ color: 'var(--text-dim)' }}>{t('Many networks need this nick registered first. The tracker explains how.')}</small>
          </label>
          <div className="seg" role="radiogroup" aria-label={t('Sign-in method')}>
            {(
              [
                ['nickserv', 'NickServ'],
                ['sasl', 'SASL'],
                ['none', t('No sign-in')],
              ] as const
            ).map(([val, label]) => (
              <button key={val} type="button" role="radio" aria-checked={auth === val} className={auth === val ? 'active' : ''} onClick={() => setAuth(val)}>
                {label}
              </button>
            ))}
          </div>
          {auth !== 'none' && (
            <div className="irc-row2">
              <label>
                {t('Account (if not the nick)')}
                <input value={authAccount} onChange={(e) => setAuthAccount(e.target.value)} autoComplete="off" spellCheck={false} />
              </label>
              <label>
                {t('Password')}
                <input type="password" value={authPassword} onChange={(e) => setAuthPassword(e.target.value)} autoComplete="new-password" placeholder={passwordSaved ? t('Saved. Leave empty to keep it.') : ''} {...v.bind('password')} />
                <FieldError v={v} name="password" />
              </label>
            </div>
          )}
          <label>
            {t('Invite command (optional)')}
            <input
              type="password"
              value={invite}
              onChange={(e) => { setInvite(e.target.value); setClearInvite(false) }}
              autoComplete="new-password"
              spellCheck={false}
              placeholder={existing?.hasInvite && !clearInvite ? t('Saved. Leave empty to keep it.') : (presetInfo?.inviteHint ?? 'BotName announce YOURKEY')}
            />
            <small style={{ color: 'var(--text-dim)' }}>
              {t('Some trackers only let you in after you message their bot with a key. Write the bot, then the message. Separate several with commas. It is stored encrypted.')}
            </small>
            {existing?.hasInvite && (
              <span className="check-hint" style={{ display: 'flex', gap: 8 }}>
                <input type="checkbox" checked={clearInvite} onChange={(e) => { setClearInvite(e.target.checked); if (e.target.checked) setInvite('') }} id="irc-clear-invite" />
                <label htmlFor="irc-clear-invite" style={{ display: 'inline' }}>{t('Remove the saved invite command')}</label>
              </span>
            )}
          </label>
          <label>
            {t('Channel to join')}
            <textarea value={channels} onChange={(e) => setChannels(e.target.value)} rows={2} placeholder="#announce" spellCheck={false} {...v.bind('channels')} />
            <FieldError v={v} name="channels" />
            <small style={{ color: 'var(--text-dim)' }}>{t('One per line. If it needs a password, add it after the name. A saved password stays unless you type a new one.')}</small>
          </label>
          <label>
            {t('Bot that posts the announcements')}
            <input value={announcers} onChange={(e) => setAnnouncers(e.target.value)} placeholder="AnnounceBot" spellCheck={false} {...v.bind('announcers')} />
            <FieldError v={v} name="announcers" />
            <small style={{ color: 'var(--text-dim)' }}>{t('Only lines from these nicks are read. Anyone else in the channel is ignored. Separate several with commas.')}</small>
          </label>
        </section>

        <section>
          <h3>{t('Reading the announcements')}</h3>
          <label>
            {t("The tracker's web address")}
            <input value={trackerUrl} onChange={(e) => setTrackerUrl(e.target.value)} placeholder="https://tracker.example.org" spellCheck={false} {...v.bind('trackerUrl', trackerUrl, setTrackerUrl)} />
            <FieldError v={v} name="trackerUrl" />
            <small style={{ color: 'var(--text-dim)' }}>{t('Your key is only ever sent to this address.')}</small>
          </label>
          <label>
            <TipLabel text={t('Describe how a line from the bot is read, so Mediarium can pull out the release name and size. A ready-made style fills these in for you.')} learnMore="indexers#writing-your-own-template">{t('Patterns')}</TipLabel>
            <textarea value={patterns} onChange={(e) => setPatterns(e.target.value)} rows={4} spellCheck={false} placeholder="(?P<releaseName>.+) - (?P<torrentSize>.+)" {...v.bind('patterns')} />
            <FieldError v={v} name="patterns" />
            <small style={{ color: 'var(--text-dim)' }}>
              {tx('One pattern per line. Name the parts you want to read: {names}. The release name is required.', {
                names: <code>(?P&lt;releaseName&gt;…) torrentSize category uploader torrentId freeleech</code>,
              })}
            </small>
          </label>
          <label className="check-hint">
            <input type="checkbox" checked={multi} onChange={(e) => setMulti(e.target.checked)} />
            <span>
              {t('Each release is announced over several lines')}
              <small>{t('The patterns are then matched one after another, one line each, and put together.')}</small>
            </span>
          </label>
          <div className="irc-row2">
            <label>
              {t('Download address')}
              <input value={downloadUrl} onChange={(e) => setDownloadUrl(e.target.value)} placeholder="/download.php?id={{ .torrentId }}&passkey={{ .passkey }}" spellCheck={false} {...v.bind('downloadUrl', downloadUrl, setDownloadUrl)} />
              <FieldError v={v} name="downloadUrl" />
            </label>
            <label>
              {t('Sizes use')}
              <select value={sizeUnits} onChange={(e) => setSizeUnits(e.target.value)}>
                <option value="binary">{t('1024 (the usual)')}</option>
                <option value="decimal">{t('1000')}</option>
              </select>
            </label>
          </div>
          <small style={{ color: 'var(--text-dim)', marginTop: -6 }}>
            {tx('Start with / and the tracker address is added. {example} is replaced by what the announcement said; any other name is one of your keys.', { example: <code>{'{{ .torrentId }}'}</code> })}
          </small>
          {keyNames.map((k) => {
            const hint = presetInfo?.keys.find((x) => x.name === k)?.hint
            const saved = existing?.keys.find((x) => x.name === k)?.hasValue
            return (
              <label key={k}>
                {t('Your {key}', { key: k })}
                <input type="password" value={keys[k] ?? ''} onChange={(e) => setKeys({ ...keys, [k]: e.target.value })} autoComplete="new-password" spellCheck={false} placeholder={saved ? t('Saved. Leave empty to keep it.') : ''} />
                {hint && <small style={{ color: 'var(--text-dim)' }}>{hint}</small>}
              </label>
            )
          })}
          <FieldError v={v} name="keys" />
          <label className="check-hint">
            <input type="checkbox" checked={freeleechOnly} onChange={(e) => setFreeleechOnly(e.target.checked)} />
            <span>
              {t('Only freeleech releases')}
              <small>{t('Needs the announcement to say so.')}</small>
            </span>
          </label>
          <div className="irc-row2">
            <label>
              <TipLabel text={t('A safety cap for this connection, so a bot that posts a flood of releases cannot fill your queue or disk.')}>{t('Most downloads an hour')}</TipLabel>
              <input inputMode="numeric" value={maxPerHour} onChange={(e) => setMaxPerHour(e.target.value)} />
            </label>
            <label>
              <TipLabel text={t('The same kind of cap per day, counted separately for each announcing bot.')}>{t('Most a day, for each bot')}</TipLabel>
              <input inputMode="numeric" value={maxPerDay} onChange={(e) => setMaxPerDay(e.target.value)} />
            </label>
          </div>

          <h3>{t('Try it')}</h3>
          <label>
            {t('Paste an announcement line')}
            <textarea
              value={testLines}
              onChange={(e) => setTestLines(e.target.value)}
              rows={multi ? 5 : 3}
              spellCheck={false}
              placeholder={presetInfo?.examples[0] ?? t('Copy a line from the announce channel')}
            />
            <small style={{ color: 'var(--text-dim)' }}>{multi ? t('Paste every line of one announcement, in order.') : t('Paste one line, exactly as the bot posts it.')}</small>
          </label>
          <div className="irc-actions">
            <button type="button" onClick={() => void runTest()} disabled={testing || !testLines.trim() || template.patterns.length === 0}>
              {testing ? t('Testing…') : t('Test the line')}
            </button>
            {presetInfo && presetInfo.examples.length > 0 && (
              <button type="button" className="btn-sm" onClick={() => setTestLines(presetInfo.examples.join('\n'))}>
                {t('Use an example')}
              </button>
            )}
          </div>
          {testError && <p className="notice notice-warn" style={{ margin: 0 }}>{testError}</p>}
          {result && <TestOutput r={result} />}
        </section>
      </div>
      <div className="irc-actions">
        <button className="primary" onClick={() => void save()} disabled={saving}>
          {saving ? t('Saving…') : t('Save')}
        </button>
        <button onClick={onCancel} disabled={saving}>
          {t('Cancel')}
        </button>
      </div>
      <FormProblem v={v} />
    </div>
  )
}

function TestOutput({ r }: { r: IrcTestResult }) {
  if (!r.matched) {
    return <p className="notice notice-warn" style={{ margin: 0 }}>{st(r.message)}</p>
  }
  return (
    <div className="irc-test-out" role="status">
      <dl>
        <dt>{t('Release')}</dt>
        <dd>{r.release}</dd>
        {r.category && (
          <>
            <dt>{t('Category')}</dt>
            <dd>{r.category}</dd>
          </>
        )}
        <dt>{t('Size')}</dt>
        <dd>{r.sizeBytes ? formatBytes(r.sizeBytes) : t('Not stated')}</dd>
        {r.uploader && (
          <>
            <dt>{t('Uploader')}</dt>
            <dd>{r.uploader}</dd>
          </>
        )}
        <dt>{t('Torrent ID')}</dt>
        <dd>{r.torrentId || t('Not stated')}</dd>
        <dt>{t('Freeleech')}</dt>
        <dd>{r.freeleech ? t('Yes') : t('No')}</dd>
        <dt>{t('Download address')}</dt>
        <dd>{r.downloadUrl ? <code>{r.downloadUrl}</code> : <span className="irc-warn-text">{r.urlProblem}</span>}</dd>
      </dl>
      <div>
        <span className={`irc-decision ${r.decision}`}>{r.decision ? DECISIONS[r.decision]?.() : ''}</span> {r.item && <strong>{r.item}. </strong>}
        {st(r.reason)}
      </div>
    </div>
  )
}
