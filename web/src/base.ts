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

// The fields in which the server sends a picture's address. Pictures it serves
// itself (book and music covers) come as "/api/...", from the domain root.
const PICTURE_FIELDS = new Set(['coverUrl', 'posterUrl', 'imageUrl', 'backdropUrl', 'logoUrl', 'profileUrl'])

/**
 * Puts base in front of every picture address on this server ("/api/...") in
 * an answer from the server, however deep it sits. The answer is changed in
 * place and returned. Addresses on other sites are left alone.
 */
export function rebasePictures<T>(base: string, data: T): T {
  if (base === '' || data === null || typeof data !== 'object') return data
  const seen: unknown[] = [data]
  while (seen.length > 0) {
    const node = seen.pop() as Record<string, unknown> | unknown[]
    if (Array.isArray(node)) {
      for (const v of node) if (v !== null && typeof v === 'object') seen.push(v)
      continue
    }
    for (const [k, v] of Object.entries(node)) {
      if (typeof v === 'string') {
        if (PICTURE_FIELDS.has(k) && v.startsWith('/api/')) node[k] = base + v
      } else if (v !== null && typeof v === 'object') seen.push(v)
    }
  }
  return data
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
