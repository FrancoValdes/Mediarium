// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { srtToVtt, subtitleTracksFor } from './subtitleTracks.ts'

test('an SRT file becomes WebVTT: header, dots in the times, no byte order mark', () => {
  const srt = '﻿1\r\n00:00:01,500 --> 00:00:03,000\r\nHello\r\n\r\n2\r\n01:02:03,004 --> 01:02:05,000\r\nBye\r\n'
  assert.equal(srtToVtt(srt), 'WEBVTT\n\n1\n00:00:01.500 --> 00:00:03.000\nHello\n\n2\n01:02:03.004 --> 01:02:05.000\nBye\n')
})

test('a file that is already WebVTT is left alone', () => {
  const vtt = 'WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n'
  assert.equal(srtToVtt(vtt), vtt)
})

test('the subtitles of a video are the ones saved next to it with a language', () => {
  const files = [
    { path: 'Film (2024).mkv', kind: 'video' },
    { path: 'Film (2024).en.srt', kind: 'subtitle' },
    { path: 'Film (2024).pt-BR.hi.srt', kind: 'subtitle' },
    { path: 'Film (2024).forced.srt', kind: 'subtitle' }, // no language: not offered
    { path: 'Film (2024).de.ass', kind: 'subtitle' }, // a format the browser can't show
    { path: 'Other (2020).en.srt', kind: 'subtitle' },
    { path: 'Film (2024).en.nfo', kind: 'nfo' },
  ]
  assert.deepEqual(subtitleTracksFor('Film (2024).mkv', files), [
    { path: 'Film (2024).en.srt', code: 'en' },
    { path: 'Film (2024).pt-BR.hi.srt', code: 'pt-BR' },
  ])
})

test('episodes in a season folder find their own subtitles', () => {
  const files = [
    { path: 'Season 01/Show - S01E01.mp4', kind: 'video' },
    { path: 'Season 01/Show - S01E01.fr.srt', kind: 'subtitle' },
    { path: 'Season 01/Show - S01E02.fr.srt', kind: 'subtitle' },
  ]
  assert.deepEqual(subtitleTracksFor('Season 01/Show - S01E01.mp4', files), [{ path: 'Season 01/Show - S01E01.fr.srt', code: 'fr' }])
})
