import { api } from './api'
import { t } from './i18n.ts'
import { withBase } from './base'

// Push notifications in the browser: whether they can work here, and
// subscribing this device with the server's key (see components/PushDevice.tsx).

export const PUSH_WORKER = withBase('/push-sw.js')

// pushSupport says whether this browser can get push notifications here, and
// if not, what to do about it.
export function pushSupport(): { ok: boolean; why?: string } {
  if (typeof window === 'undefined') return { ok: false }
  if (!window.isSecureContext) {
    return {
      ok: false,
      why: t('Push only works when Mediarium is opened over https (or at localhost on the server itself). Open it through your secure address, for example a reverse proxy, then come back here.'),
    }
  }
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent)
  if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) {
    return {
      ok: false,
      why: ios
        ? t('On an iPhone or iPad, first add Mediarium to the Home Screen (Share > Add to Home Screen), open it from there, and come back to this page.')
        : t("This browser can't receive push notifications. Try Chrome, Edge, Firefox or Safari."),
    }
  }
  return { ok: true }
}

// deviceName guesses a name for this device ("iPhone", "Chrome on Windows").
export function deviceName(): string {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return 'iPhone'
  if (/iPad/.test(ua)) return 'iPad'
  if (/Android/.test(ua)) return 'Android phone'
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /Windows/.test(ua) ? 'Windows' : /Mac OS X/.test(ua) ? 'Mac' : /Linux/.test(ua) ? 'Linux' : ''
  return os ? `${browser} on ${os}` : browser
}

function keyBytes(base64url: string): Uint8Array {
  const pad = '='.repeat((4 - (base64url.length % 4)) % 4)
  const raw = atob((base64url + pad).replace(/-/g, '+').replace(/_/g, '/'))
  return Uint8Array.from(raw, (c) => c.charCodeAt(0))
}

// subscribeThisDevice asks permission and returns the subscription as JSON.
export async function subscribeThisDevice(): Promise<string> {
  const permission = await Notification.requestPermission()
  if (permission !== 'granted') {
    throw new Error(
      permission === 'denied'
        ? t("Notifications are blocked for Mediarium in this browser. Allow them in the browser's site settings, then try again.")
        : t('Press Allow when the browser asks, to get notifications here.'),
    )
  }
  const reg = await navigator.serviceWorker.register(PUSH_WORKER)
  await navigator.serviceWorker.ready
  const { publicKey } = await api.pushKey()
  const existing = await reg.pushManager.getSubscription()
  if (existing) {
    // A subscription made with another key can't be reused.
    const opts = existing.options.applicationServerKey
    const same = opts && new Uint8Array(opts).join(',') === keyBytes(publicKey).join(',')
    if (same) return JSON.stringify(existing.toJSON())
    await existing.unsubscribe()
  }
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) as BufferSource })
  return JSON.stringify(sub.toJSON())
}
