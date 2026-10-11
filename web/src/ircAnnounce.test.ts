import assert from 'node:assert/strict'
import { test } from 'node:test'
import { groupNames, neededKeys, parseChannels, parseList } from './ircAnnounce.ts'

const single = {
  multi: false,
  sizeUnits: 'binary',
  patterns: ['\\[(?P<category>.+?)] (?P<releaseName>.*?) - (?P<torrentSize>.*)', '(?<torrentId>\\d+)'],
  downloadUrl: '/download.php/{{ .torrentId }}/{{ .releaseName }}.torrent?torrent_pass={{ .passkey }}&k={{.authkey}}',
}

test('group names are read from every pattern', () => {
  assert.deepEqual(groupNames(single.patterns).sort(), ['category', 'releaseName', 'torrentId', 'torrentSize'])
})

test('keys are the names the address uses that no pattern captures', () => {
  assert.deepEqual(neededKeys(single), ['passkey', 'authkey'])
  assert.deepEqual(neededKeys({ ...single, downloadUrl: '/x/{{ .torrentId }}' }), [])
})

test('channels are read one per line, with an optional password', () => {
  assert.deepEqual(parseChannels('#announce\n#vip  secret\n\n'), [
    { name: '#announce', key: '' },
    { name: '#vip', key: 'secret' },
  ])
  assert.deepEqual(parseChannels('#a, #b'), [
    { name: '#a', key: '' },
    { name: '#b', key: '' },
  ])
})

test('nick lists accept commas, spaces and lines', () => {
  assert.deepEqual(parseList('Bot1, Bot2\nBot3'), ['Bot1', 'Bot2', 'Bot3'])
  assert.deepEqual(parseList(''), [])
})
