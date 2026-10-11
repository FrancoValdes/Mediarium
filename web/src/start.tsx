import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './design.css'
import './setup.css'
import './projection.css'
import './mobile.css'
import { demoEnabled, installDemo } from './demo'
import App from './App.tsx'
import { ToastProvider } from './components/Toast'
import { ConfirmProvider } from './components/ConfirmProvider'
import { PUSH_WORKER } from './push'

if (demoEnabled()) installDemo()

// Mediarium's only service worker is the push one (push-sw.js), which caches
// nothing. One left behind by another app that once ran on the same address
// would answer API calls from its own cache and show stale data, so remove
// any other we find and reload once.
if ('serviceWorker' in navigator) {
  navigator.serviceWorker
    .getRegistrations()
    .then(async (regs) => {
      const isPush = (r: ServiceWorkerRegistration) => [r.active, r.waiting, r.installing].some((w) => w?.scriptURL.endsWith(PUSH_WORKER))
      const stale = regs.filter((r) => !isPush(r))
      if (stale.length === 0) return
      await Promise.all(stale.map((r) => r.unregister()))
      const ctrl = navigator.serviceWorker.controller
      if (ctrl && !ctrl.scriptURL.endsWith(PUSH_WORKER)) location.reload()
    })
    .catch(() => {
      // ignore — not having permission to look is fine
    })
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ToastProvider>
      <ConfirmProvider>
        <App />
      </ConfirmProvider>
    </ToastProvider>
  </StrictMode>,
)
