import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isStaleWorker } from './staleWorker.ts'

test('a worker of another app at the root or at our base is stale', () => {
  assert.equal(isStaleWorker('https://h.example/', ['https://h.example/sw.js'], '', '/push-sw.js'), true)
  assert.equal(isStaleWorker('https://h.example/', ['https://h.example/sw.js'], '/m', '/m/push-sw.js'), true)
  assert.equal(isStaleWorker('https://h.example/m/', [undefined, 'https://h.example/m/old-sw.js'], '/m', '/m/push-sw.js'), true)
})

test('our own worker and workers of apps under other paths are kept', () => {
  assert.equal(isStaleWorker('https://h.example/', ['https://h.example/push-sw.js'], '', '/push-sw.js'), false)
  assert.equal(isStaleWorker('https://h.example/m/', ['https://h.example/m/push-sw.js'], '/m', '/m/push-sw.js'), false)
  assert.equal(isStaleWorker('https://h.example/jellyfin/', ['https://h.example/jellyfin/sw.js'], '/m', '/m/push-sw.js'), false)
  assert.equal(isStaleWorker('https://h.example/jellyfin/', ['https://h.example/jellyfin/sw.js'], '', '/push-sw.js'), false)
  assert.equal(isStaleWorker('https://h.example/mx/', ['https://h.example/mx/sw.js'], '/m', '/m/push-sw.js'), false)
  assert.equal(isStaleWorker('not a url', ['x'], '', '/push-sw.js'), false)
})
