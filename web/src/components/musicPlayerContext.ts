import { createContext, useContext } from 'react'
import { withBase } from '../base'
import type { PlayerSettings, PlayerTrack, QueuedTrack, Repeat } from '../musicQueue'

export type { PlayerTrack, QueuedTrack, Repeat }

// Which panel above the player bar is open.
export type PanelTab = 'queue' | 'lyrics' | 'settings'

export interface PlayerApi {
  current: QueuedTrack | null
  queue: QueuedTrack[]
  index: number
  playing: boolean
  time: number
  duration: number
  volume: number
  shuffle: boolean
  repeat: Repeat
  problem: string
  // Starts these tracks as the queue.
  playQueue: (tracks: PlayerTrack[], start?: number) => void
  // Puts tracks right after the one playing / at the end of the queue.
  playNextTracks: (tracks: PlayerTrack[]) => void
  addToQueue: (tracks: PlayerTrack[]) => void
  moveInQueue: (from: number, to: number) => void
  removeFromQueue: (at: number) => void
  clearQueue: (keepCurrent: boolean) => void
  toggle: () => void
  next: () => void
  prev: () => void
  seek: (seconds: number) => void
  setVolume: (v: number) => void
  setShuffle: (on: boolean) => void
  cycleRepeat: () => void
  close: () => void
  jumpTo: (index: number) => void
  // popOut moves playback to a window of its own (false when the browser
  // refused the window); handBack, from that window, gives it back.
  popOut: () => boolean
  handBack: () => void
  popout: boolean
  // taken is set in the pop-out window when the app window started something else.
  taken: boolean
  // Crossfade and ReplayGain, kept on this device.
  settings: PlayerSettings
  setSettings: (patch: Partial<PlayerSettings>) => void
  // False on iPhone and iPad, where only the buttons on the side change the volume.
  volumeWorks: boolean
  panel: PanelTab | null
  setPanel: (tab: PanelTab | null) => void
  // The account's ListenBrainz connection (null until known), and a way to
  // read it again after it was changed in the profile.
  scrobbling: { connected: boolean; enabled: boolean } | null
  refreshScrobbling: () => void
}

export const PlayerContext = createContext<PlayerApi | null>(null)

// useMusicPlayer is null outside the app shell (the Books app has its own player).
export function useMusicPlayer(): PlayerApi | null {
  return useContext(PlayerContext)
}

export const trackUrl = (id: number) => withBase(`/api/music/tracks/${id}/play`)
export const albumCoverUrl = (albumId: number) => withBase(`/api/music/albums/${albumId}/cover`)
