import assert from 'node:assert/strict'
import { test } from 'node:test'
import { saveOrder } from './autosaveOrder.ts'

test('a read with no save around it counts', () => {
  const o = saveOrder()
  assert.equal(o.readCounts(o.readStarted()), true)
})

test('a read that started before a save finished is ignored', () => {
  const o = saveOrder()
  const read = o.readStarted() // the tab comes back into view...
  const save = o.saveStarted() // ...in the same click that flips a switch
  o.saveEnded()
  assert.equal(o.isNewest(save), true)
  assert.equal(o.readCounts(read), false) // the read answers last, with the old value
  assert.equal(o.readCounts(o.readStarted()), true) // a later read is fine
})

test('a read that answers while a save is running is ignored', () => {
  const o = saveOrder()
  o.saveStarted()
  const read = o.readStarted()
  assert.equal(o.readCounts(read), false)
})

test('only the newest of two overlapping saves decides', () => {
  const o = saveOrder()
  const first = o.saveStarted()
  const second = o.saveStarted()
  assert.equal(o.isNewest(first), false)
  assert.equal(o.isNewest(second), true)
  o.saveEnded()
  o.saveEnded()
  o.saveEnded() // never goes below zero
  assert.equal(o.readCounts(o.readStarted()), true)
})
