import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pollPin } from './pinPoll.ts'

// A pretend clock: run() fires the waiting timer and lets its promise settle.
function fakeTime() {
  let clock = 0
  let next: (() => void) | null = null
  return {
    now: () => clock,
    later: (fn: () => void, ms: number) => {
      next = () => {
        clock += ms
        fn()
      }
      return 1
    },
    cancel: () => {
      next = null
    },
    async run() {
      const fn = next
      next = null
      fn?.()
      for (let i = 0; i < 5; i++) await Promise.resolve()
    },
    pending: () => next !== null,
  }
}

test('stops once signed in and reports it once', async () => {
  const time = fakeTime()
  const answers: (string | undefined)[] = [undefined, undefined, 'ok']
  const done: string[] = []
  pollPin({ check: async () => answers.shift(), lifeSeconds: 600, onDone: (r) => done.push(r), onExpired: () => assert.fail('expired'), onError: () => assert.fail('error'), ...time })
  for (let i = 0; i < 5; i++) await time.run()
  assert.deepEqual(done, ['ok'])
  assert.equal(time.pending(), false)
})

test('gives up when the PIN has run out', async () => {
  const time = fakeTime()
  let expired = 0
  let asked = 0
  pollPin({ check: async () => (asked++, undefined), lifeSeconds: 30, everyMs: 10_000, onDone: () => assert.fail('done'), onExpired: () => expired++, onError: () => assert.fail('error'), ...time })
  for (let i = 0; i < 10; i++) await time.run()
  assert.equal(expired, 1)
  assert.equal(asked, 3) // at 10, 20 and 30 seconds; at 40 the PIN (30 s and a little) is over
  assert.equal(time.pending(), false)
})

test('an error stops it, and so does stopping it', async () => {
  const time = fakeTime()
  const errors: unknown[] = []
  pollPin({ check: async () => Promise.reject(new Error('gone')), lifeSeconds: 600, onDone: () => assert.fail('done'), onExpired: () => assert.fail('expired'), onError: (e) => errors.push(e), ...time })
  await time.run()
  await time.run()
  assert.equal(errors.length, 1)

  const t2 = fakeTime()
  const stop = pollPin({ check: async () => 'late', lifeSeconds: 600, onDone: () => assert.fail('done after stop'), onExpired: () => assert.fail('expired'), onError: () => assert.fail('error'), ...t2 })
  stop()
  await t2.run()
})
