// The browser side of passkeys (WebAuthn): turning the server's options into
// what navigator.credentials wants, and the browser's answer back into JSON.
// The server sends binary values as base64url text.

import { t } from './i18n.ts'

export function b64uToBytes(s: string): Uint8Array<ArrayBuffer> {
  const pad = '='.repeat((4 - (s.length % 4)) % 4)
  const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/') + pad)
  const out = new Uint8Array(new ArrayBuffer(bin.length))
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function bytesToB64u(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf)
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// True when this browser can do passkeys at all (it also needs a secure page: https or localhost).
export function passkeysSupported(): boolean {
  return typeof window !== 'undefined' && window.isSecureContext && 'PublicKeyCredential' in window && !!navigator.credentials
}

type Descriptor = { id: string; type: string; transports?: string[] }

function decodeDescriptors(list: unknown): PublicKeyCredentialDescriptor[] | undefined {
  if (!Array.isArray(list)) return undefined
  return (list as Descriptor[]).map((d) => ({ ...d, id: b64uToBytes(d.id) }) as PublicKeyCredentialDescriptor)
}

// What a person is told when the browser stops a passkey step.
export function passkeyProblem(e: unknown, host?: string): string {
  const name = e instanceof DOMException ? e.name : ''
  switch (name) {
    case 'NotAllowedError':
    case 'AbortError':
      return t('The passkey step was cancelled or timed out. Try again.')
    case 'SecurityError':
      return host
        ? t('Your browser only uses passkeys on pages opened at {host}. Open Mediarium at that address and try again.', { host })
        : t("Your browser won't use passkeys on this page. Open Mediarium at its public address and try again.")
    case 'InvalidStateError':
      return t('This device already has a passkey for your account.')
    case 'NotSupportedError':
      return t("This device can't make a passkey the way Mediarium needs (it must be able to keep it for this site and check it is you).")
  }
  return e instanceof Error ? e.message : String(e)
}

// Makes a passkey from the options the server sent.
export async function createPasskey(options: { publicKey: Record<string, unknown> }): Promise<unknown> {
  const pk = options.publicKey as Record<string, any> // eslint-disable-line @typescript-eslint/no-explicit-any
  const publicKey = {
    ...pk,
    challenge: b64uToBytes(pk.challenge),
    user: { ...pk.user, id: b64uToBytes(pk.user.id) },
    excludeCredentials: decodeDescriptors(pk.excludeCredentials),
  } as unknown as PublicKeyCredentialCreationOptions
  const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null
  if (!cred) throw new DOMException('cancelled', 'NotAllowedError')
  const r = cred.response as AuthenticatorAttestationResponse
  return {
    id: cred.id,
    rawId: bytesToB64u(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response: {
      clientDataJSON: bytesToB64u(r.clientDataJSON),
      attestationObject: bytesToB64u(r.attestationObject),
      transports: r.getTransports ? r.getTransports() : [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  }
}

// Signs in with a passkey the browser picks from the options the server sent.
export async function getPasskey(options: { publicKey: Record<string, unknown> }): Promise<unknown> {
  const pk = options.publicKey as Record<string, any> // eslint-disable-line @typescript-eslint/no-explicit-any
  const publicKey = {
    ...pk,
    challenge: b64uToBytes(pk.challenge),
    allowCredentials: decodeDescriptors(pk.allowCredentials),
  } as unknown as PublicKeyCredentialRequestOptions
  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null
  if (!cred) throw new DOMException('cancelled', 'NotAllowedError')
  const r = cred.response as AuthenticatorAssertionResponse
  return {
    id: cred.id,
    rawId: bytesToB64u(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response: {
      clientDataJSON: bytesToB64u(r.clientDataJSON),
      authenticatorData: bytesToB64u(r.authenticatorData),
      signature: bytesToB64u(r.signature),
      userHandle: r.userHandle ? bytesToB64u(r.userHandle) : undefined,
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  }
}
