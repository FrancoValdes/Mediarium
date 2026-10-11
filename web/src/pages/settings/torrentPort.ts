// What the torrent port is doing, in plain words, for Settings > Downloading.
// Kept apart from the page so the wording can be tested.
import { t } from '../../i18n.ts'

export interface TorrentPortInfo {
  state?: string
  listenPort: number
  vpnState?: string
  blockedByVpn: boolean
  listening?: boolean
  activePort?: number
  incomingSeen?: boolean
}

export interface PortStatusText {
  tone: 'good' | 'info' | 'warn'
  title: string
  text: string
}

export function portStatus(info: TorrentPortInfo | null): PortStatusText | null {
  if (!info || info.state === 'disabled') return null
  if (info.vpnState === 'connected' || info.vpnState === 'connecting') {
    return {
      tone: 'info',
      title: t('Not used while your VPN is on.'),
      text: t("Torrents go through the VPN, so this port isn't used. Port forwarding through a VPN isn't supported."),
    }
  }
  if (info.blockedByVpn) {
    return {
      tone: 'warn',
      title: t('Waiting for your VPN.'),
      text: t('Torrents are on hold until it connects, so this port is closed.'),
    }
  }
  if (!info.listening) {
    return {
      tone: 'info',
      title: t('Closed for now.'),
      text: t('It opens when a torrent is downloading or seeding.'),
    }
  }
  // Ports are passed as text so they never get a thousands separator.
  const port = String(info.activePort || info.listenPort)
  const moved =
    info.activePort && info.activePort !== info.listenPort
      ? ' ' + t('Port {port} was already in use, so Mediarium is using {active} instead.', { port: String(info.listenPort), active: String(info.activePort) })
      : ''
  if (info.incomingSeen) {
    return {
      tone: moved ? 'warn' : 'good',
      title: t('Listening on port {port}.', { port }),
      text: t('Another peer has connected to you, so the port works from the internet.') + moved,
    }
  }
  return {
    tone: moved ? 'warn' : 'info',
    title: t('Listening on port {port}.', { port }),
    text: t('No peer has connected yet, which is normal at first. If it stays that way, forward this port (TCP and UDP) on your router.') + moved,
  }
}
