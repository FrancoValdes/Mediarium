// Mediarium can be served under a sub-path behind a reverse proxy, such as
// /mediarium (the URL_BASE setting of the container). The server puts that
// prefix in a <meta name="mediarium-base"> tag of the page, so the same build
// works under any prefix. Without the tag the prefix is empty and every
// address is what it always was.

/** Cleans up what the page's tag holds: "" or a path like "/mediarium" with no slash at the end. */
export function cleanBase(raw: string | null | undefined): string {
  const trimmed = (raw ?? '').trim().replace(/\/+$/, '')
  return trimmed.startsWith('/') ? trimmed : ''
}

/** Puts base in front of an address on this server that starts with a slash. Anything else comes back unchanged. */
export function prefixed(base: string, path: string): string {
  if (base === '' || !path.startsWith('/') || path.startsWith('//')) return path
  return base + path
}

/** The reverse of prefixed: the address without base. */
export function unprefixed(base: string, path: string): string {
  if (base !== '' && (path === base || path.startsWith(base + '/'))) return path.slice(base.length) || '/'
  return path
}

function readBase(): string {
  if (typeof document === 'undefined') return ''
  return cleanBase(document.querySelector('meta[name="mediarium-base"]')?.getAttribute('content'))
}

/** The prefix Mediarium is served under: "" or something like "/mediarium". */
export const BASE_PATH = readBase()

/** '/api/version' becomes '/mediarium/api/version' under a prefix. */
export const withBase = (path: string): string => prefixed(BASE_PATH, path)

/** The address without the prefix. */
export const withoutBase = (path: string): string => unprefixed(BASE_PATH, path)
