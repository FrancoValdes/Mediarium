// The page content scrolls inside the app shell's .content area, not the
// window, so going to the top means scrolling that element (the window too,
// for pages shown outside the shell, like sign-in and the wizard).
export function scrollToTop(smooth = false) {
  const behavior: ScrollBehavior = smooth ? 'smooth' : 'auto'
  const go = (b: ScrollBehavior) => {
    document.querySelector('.content')?.scrollTo({ top: 0, behavior: b })
    window.scrollTo({ top: 0, behavior: b })
  }
  go(behavior)
  if (smooth) return
  // iOS Safari ignores a jump to the top while the finger's momentum is still carrying the page, and a page that was
  // still filling in can push it back down: ask again once things have settled.
  requestAnimationFrame(() => go('auto'))
  window.setTimeout(() => {
    const el = document.querySelector('.content')
    if ((el?.scrollTop ?? 0) > 0 || window.scrollY > 0) go('auto')
  }, 120)
}
