// Run with: npm test
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeLine, parseLrc } from './lrc.ts'

test('reads times and words, in time order', () => {
  const p = parseLrc('[ar:Someone]\n[ti:A song]\n[00:12.50] First\n[00:05.00]Zero\n[01:02.123]Last one\n')
  assert.equal(p.synced, true)
  assert.deepEqual(p.lines, [
    { time: 5, text: 'Zero' },
    { time: 12.5, text: 'First' },
    { time: 62.123, text: 'Last one' },
  ])
})

test('time stamp forms', () => {
  const p = parseLrc('[0:01]a\n[00:02.5]b\n[00:03.05]c\n[00:04:50]d\n[100:00.00]e')
  assert.deepEqual(p.lines.map((l) => l.time), [1, 2.5, 3.05, 4.5, 6000])
})

test('one line with several times is repeated at each time', () => {
  const p = parseLrc('[00:10.00][01:30.00]Chorus\n[00:20.00]Verse')
  assert.deepEqual(p.lines.map((l) => `${l.time}:${l.text}`), ['10:Chorus', '20:Verse', '90:Chorus'])
})

test('the offset tag shifts every line', () => {
  const p = parseLrc('[offset:+500]\n[00:10.00]a\n[00:20.00]b')
  assert.deepEqual(p.lines.map((l) => l.time), [9.5, 19.5])
  const q = parseLrc('[offset:-1000]\n[00:10.00]a\n[00:20.00]b')
  assert.deepEqual(q.lines.map((l) => l.time), [11, 21])
  const early = parseLrc('[offset:+5000]\n[00:01.00]a\n[00:20.00]b')
  assert.equal(early.lines[0].time, 0, 'never before the start')
})

test('word-by-word times and stray spaces are cleaned out', () => {
  const p = parseLrc('[00:01.00]<00:01.00>Hello <00:01.50>there   friend\r\n[00:03.00]  \r\n[00:04.00]x')
  assert.equal(p.lines[0].text, 'Hello there friend')
  assert.equal(p.lines[1].text, '', 'an empty line is a pause and is kept')
})

test('plain words come back as plain lines', () => {
  const p = parseLrc('First line\n\nSecond line\n[00:01.00] only one stamp')
  assert.equal(p.synced, false)
  assert.deepEqual(p.lines.map((l) => l.text), ['First line', 'Second line', 'only one stamp'])
  assert.equal(parseLrc('').synced, false)
  assert.deepEqual(parseLrc('').lines, [])
  assert.deepEqual(parseLrc('[00:01.00] alone').lines, [{ time: 0, text: 'alone' }])
})

test('a time in the middle of a line is part of the words', () => {
  const p = parseLrc('[00:01.00]I said [00:05.00] hello\n[00:09.00]b')
  assert.equal(p.lines[0].text, 'I said [00:05.00] hello')
})

test('finding the line that is being sung', () => {
  const { lines } = parseLrc('[00:05.00]a\n[00:10.00]b\n[00:10.00]c\n[00:20.00]d')
  assert.equal(activeLine(lines, 0), -1)
  assert.equal(activeLine(lines, 4.99), -1)
  assert.equal(activeLine(lines, 5), 0)
  assert.equal(activeLine(lines, 9.9), 0)
  assert.equal(activeLine(lines, 10), 2, 'two lines at the same time: the later one')
  assert.equal(activeLine(lines, 19), 2)
  assert.equal(activeLine(lines, 9999), 3)
  assert.equal(activeLine([], 5), -1)
})
