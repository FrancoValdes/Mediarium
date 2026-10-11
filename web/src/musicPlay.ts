import { api, type MusicAlbum } from './api'
import type { PlayerTrack } from './components/MusicPlayer'

// The tracks of an album that are in the library, as the player wants them.
export function playable(album: MusicAlbum, artist: string): PlayerTrack[] {
  return (album.tracks ?? [])
    .filter((tr) => tr.hasFile)
    .map((tr) => ({ id: tr.id, title: tr.title, artist: artist || album.artistName || '', album: album.title, albumId: album.id, lengthMs: tr.lengthMs }))
}

// Everything of an artist that is in the library, the newest album first.
export async function artistQueue(artistId: number): Promise<PlayerTrack[]> {
  const artist = await api.getArtist(artistId)
  const done = (artist.albums ?? []).filter((a) => a.status === 'downloaded').sort((a, b) => b.releaseDate.localeCompare(a.releaseDate))
  const full = await Promise.all(done.map((a) => api.getAlbum(a.id)))
  return full.flatMap((al) => playable(al, artist.name))
}
