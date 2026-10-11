import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from './api'
import { movieState, seriesState, type ItemStateInput } from './components/state'
import { useLive } from './useLive'

export interface Owned {
  movies: Map<number, ItemStateInput>
  shows: Map<number, ItemStateInput>
  reload: () => void
}

// Which TMDB titles are already in the library, and their state (wanted,
// downloading, downloaded), kept live so Discover cards update on their own.
export function useOwned(ms = 15000): Owned {
  const [movies, setMovies] = useState<Map<number, ItemStateInput>>(new Map())
  const [shows, setShows] = useState<Map<number, ItemStateInput>>(new Map())

  // The same answer keeps the same Map, so the cards on Discover are not redrawn every poll for nothing.
  // The lists come back as the very same objects when nothing changed on the server; then there is nothing to rebuild.
  const seen = useRef<{ movies?: [unknown, unknown]; shows?: [unknown, unknown] }>({})
  const reload = useCallback(() => {
    const queue = api.listQueue().catch(() => [])
    const same = (next: Map<number, ItemStateInput>) => (prev: Map<number, ItemStateInput>) => (JSON.stringify([...prev]) === JSON.stringify([...next]) ? prev : next)
    Promise.all([queue, api.listMovies()]).then(([q, m]) => {
      if (seen.current.movies?.[0] === q && seen.current.movies[1] === m) return
      seen.current.movies = [q, m]
      setMovies(same(new Map(m.map((x) => [x.tmdbId, { id: x.id, state: movieState(x, q) }]))))
    }).catch(() => undefined)
    Promise.all([queue, api.listSeries()]).then(([q, s]) => {
      if (seen.current.shows?.[0] === q && seen.current.shows[1] === s) return
      seen.current.shows = [q, s]
      setShows(same(new Map(s.map((x) => [x.tmdbId, { id: x.id, state: seriesState(x, q) }]))))
    }).catch(() => undefined)
  }, [])

  useEffect(reload, [reload])
  useLive(reload, ms)
  return { movies, shows, reload }
}
