// Run with: npm test
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  addToEnd,
  clearQueue,
  moveTrack,
  parseQueue,
  parseSettings,
  planNext,
  playNext,
  removeTrack,
  serializeQueue,
  withUids,
  type PlayerTrack,
  type QueueState,
} from './musicQueue.ts'

const song = (id: number, extra: Partial<PlayerTrack> = {}): PlayerTrack => ({ id, title: `Song ${id}`, artist: 'Band', album: 'Album', albumId: 1, lengthMs: 180000, ...extra })
const state = (ids: number[], index: number): QueueState => ({ queue: withUids(ids.map((i) => song(i))), index })
const ids = (s: QueueState) => s.queue.map((t) => t.id)

test('every place in the queue has its own id, even for the same song', () => {
  const q = withUids([song(1), song(1), song(2)])
  assert.equal(new Set(q.map((t) => t.uid)).size, 3)
})

test('play next goes right after the current track, in the order given', () => {
  const s = playNext(state([1, 2, 3], 1), [song(10), song(11)])
  assert.deepEqual(ids(s), [1, 2, 10, 11, 3])
  assert.equal(s.index, 1)
  assert.deepEqual(ids(playNext(state([], -1), [song(5), song(6)])), [5, 6])
  assert.equal(playNext(state([], -1), [song(5)]).index, 0)
  const same = state([1, 2], 0)
  assert.equal(playNext(same, []), same)
})

test('add to queue goes at the end', () => {
  const s = addToEnd(state([1, 2], 0), [song(9)])
  assert.deepEqual(ids(s), [1, 2, 9])
  assert.equal(s.index, 0)
  assert.deepEqual(ids(addToEnd(state([], -1), [song(9)])), [9])
})

test('moving a track keeps the playing track playing', () => {
  const cases: Array<{ name: string; ids: number[]; index: number; from: number; to: number; want: number[]; wantIndex: number }> = [
    { name: 'move the playing track down', ids: [1, 2, 3, 4], index: 1, from: 1, to: 3, want: [1, 3, 4, 2], wantIndex: 3 },
    { name: 'move a track from before to after the playing one', ids: [1, 2, 3, 4], index: 2, from: 0, to: 3, want: [2, 3, 4, 1], wantIndex: 1 },
    { name: 'move a track from after to before the playing one', ids: [1, 2, 3, 4], index: 1, from: 3, to: 0, want: [4, 1, 2, 3], wantIndex: 2 },
    { name: 'move a track from after to onto the playing place', ids: [1, 2, 3, 4], index: 1, from: 3, to: 1, want: [1, 4, 2, 3], wantIndex: 2 },
    { name: 'move around without crossing it', ids: [1, 2, 3, 4], index: 0, from: 2, to: 3, want: [1, 2, 4, 3], wantIndex: 0 },
    { name: 'move from before to before', ids: [1, 2, 3, 4, 5], index: 4, from: 0, to: 2, want: [2, 3, 1, 4, 5], wantIndex: 4 },
  ]
  for (const c of cases) {
    const s = moveTrack(state(c.ids, c.index), c.from, c.to)
    assert.deepEqual(ids(s), c.want, c.name)
    assert.equal(s.queue[s.index].id, c.ids[c.index], `${c.name}: the same track is still playing`)
    assert.equal(s.index, c.wantIndex, c.name)
  }
  const unchanged = state([1, 2], 0)
  assert.equal(moveTrack(unchanged, 0, 0), unchanged)
  assert.equal(moveTrack(unchanged, 0, 5), unchanged)
  assert.equal(moveTrack(unchanged, -1, 1), unchanged)
})

test('removing a track', () => {
  let r = removeTrack(state([1, 2, 3], 1), 0)
  assert.deepEqual(ids(r), [2, 3])
  assert.equal(r.index, 0)
  assert.equal(r.currentRemoved, false)

  r = removeTrack(state([1, 2, 3], 1), 2)
  assert.deepEqual(ids(r), [1, 2])
  assert.equal(r.index, 1)
  assert.equal(r.currentRemoved, false)

  r = removeTrack(state([1, 2, 3], 1), 1) // the playing one: the next one takes its place
  assert.deepEqual(ids(r), [1, 3])
  assert.equal(r.index, 1)
  assert.equal(r.currentRemoved, true)

  r = removeTrack(state([1, 2, 3], 2), 2) // the last one: the new last one
  assert.equal(r.index, 1)
  assert.equal(r.currentRemoved, true)

  r = removeTrack(state([1], 0), 0)
  assert.deepEqual(r.queue, [])
  assert.equal(r.index, -1)
  assert.equal(r.currentRemoved, true)

  const s = state([1, 2], 0)
  assert.equal(removeTrack(s, 7).queue, s.queue)
})

test('clearing the queue, with or without the playing track', () => {
  assert.deepEqual(clearQueue(state([1, 2, 3], 1), false), { queue: [], index: -1 })
  const kept = clearQueue(state([1, 2, 3], 1), true)
  assert.deepEqual(ids(kept), [2])
  assert.equal(kept.index, 0)
  assert.deepEqual(clearQueue(state([], -1), true), { queue: [], index: -1 })
})

test('what plays next', () => {
  assert.equal(planNext(0, -1, false, 'off'), -1)
  assert.equal(planNext(3, 0, false, 'off'), 1)
  assert.equal(planNext(3, 2, false, 'off'), -1, 'the end of the queue stops')
  assert.equal(planNext(3, 2, false, 'all'), 0, 'repeat all starts over')
  assert.equal(planNext(3, 1, false, 'one'), 1, 'repeat one stays')
  assert.equal(planNext(1, 0, true, 'off'), -1, 'shuffling one track has nothing to pick')
  assert.equal(planNext(1, 0, true, 'all'), 0)
  // Shuffle never picks the current place, and can reach every other one.
  for (const index of [0, 1, 4]) {
    const seen = new Set<number>()
    for (let i = 0; i < 100; i++) {
      const n = planNext(5, index, true, 'off', () => i / 100)
      assert.notEqual(n, index)
      assert.ok(n >= 0 && n < 5)
      seen.add(n)
    }
    assert.equal(seen.size, 4)
  }
  assert.ok(planNext(4, 1, true, 'off', () => 0.999999) < 4)
})

test('the queue survives being saved and read back', () => {
  const s = state([5, 6, 7], 2)
  const text = serializeQueue({ queue: s.queue, index: s.index, time: 83.7, shuffle: true, repeat: 'all' })
  assert.ok(!text.includes('uid'), 'the places are renumbered on load')
  const back = parseQueue(text)
  assert.ok(back)
  assert.deepEqual(back.queue.map((t) => t.id), [5, 6, 7])
  assert.equal(back.index, 2)
  assert.equal(back.time, 83)
  assert.equal(back.shuffle, true)
  assert.equal(back.repeat, 'all')
  assert.equal(back.queue[0].title, 'Song 5')
})

test('damaged saved data never breaks the player', () => {
  for (const raw of [null, '', 'nope', '[]', '{}', '{"v":2,"queue":[]}', '{"v":1,"queue":"x"}', '{"v":1,"queue":[]}', '{"v":1,"queue":[{"id":"a"}]}', '{"v":1,"queue":[null,5,{"title":"x"}]}']) {
    assert.equal(parseQueue(raw), null, String(raw))
  }
  // Bad tracks are dropped and the playing place follows its track.
  const raw = JSON.stringify({
    v: 1,
    queue: [{ id: 1, title: 'A', albumId: 1 }, { id: 'x' }, { id: 3, title: 'C', albumId: 1, artist: 'Z', album: 'Y', lengthMs: 5 }, null, { id: 5, title: 'E', albumId: 2 }],
    index: 4,
    time: -4,
    shuffle: 'yes',
    repeat: 'sideways',
  })
  const back = parseQueue(raw)
  assert.ok(back)
  assert.deepEqual(back.queue.map((t) => t.id), [1, 3, 5])
  assert.equal(back.index, 2)
  assert.equal(back.time, 0)
  assert.equal(back.shuffle, false)
  assert.equal(back.repeat, 'off')
  assert.equal(back.queue[0].artist, '')
  // The playing track itself was bad: start at the top.
  assert.equal(parseQueue(JSON.stringify({ v: 1, queue: [{ id: 1, title: 'A', albumId: 1 }, { id: 'x' }, { id: 3, title: 'C', albumId: 1 }], index: 1 }))?.index, 0)
  // An index out of range is pulled back in.
  assert.equal(parseQueue(JSON.stringify({ v: 1, queue: [{ id: 1, title: 'A', albumId: 1 }], index: 40 }))?.index, 0)
})

test('very long queues are cut when saved', () => {
  const many = Array.from({ length: 1500 }, (_, i) => song(i + 1))
  const back = parseQueue(serializeQueue({ queue: withUids(many), index: 1200, time: 0, shuffle: false, repeat: 'off' }))
  assert.ok(back)
  assert.equal(back.queue.length, 1000)
  assert.equal(back.index, 0, 'the playing track was cut, so it starts from the top')
})

test('player settings on this device', () => {
  assert.deepEqual(parseSettings(null), { crossfade: 0, replayGain: 'off' })
  assert.deepEqual(parseSettings('{"crossfade":6,"replayGain":"album"}'), { crossfade: 6, replayGain: 'album' })
  assert.deepEqual(parseSettings('{"crossfade":99,"replayGain":"track"}'), { crossfade: 12, replayGain: 'track' })
  assert.deepEqual(parseSettings('{"crossfade":-3,"replayGain":"loud"}'), { crossfade: 0, replayGain: 'off' })
  assert.deepEqual(parseSettings('{"crossfade":"a"}'), { crossfade: 0, replayGain: 'off' })
  assert.deepEqual(parseSettings('garbage'), { crossfade: 0, replayGain: 'off' })
})
