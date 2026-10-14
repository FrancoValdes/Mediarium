// Which service workers on this address are left over from another app and
// get in Mediarium's way. Only a worker that controls Mediarium's own pages
// counts: one registered for the domain root, or for Mediarium's own base.
// A worker of another app on the same domain under its own path (/jellyfin/)
// never touches Mediarium and is left alone.

/** True when a worker with this scope and these scripts should be removed. */
export function isStaleWorker(scope: string, scripts: (string | undefined)[], base: string, pushWorker: string): boolean {
  if (scripts.some((s) => s?.endsWith(pushWorker))) return false
  let path: string
  try {
    path = new URL(scope).pathname
  } catch {
    return false
  }
  return path === '/' || path === base + '/'
}
