// Keeps the answers of a setting that saves the moment it changes in order.
// Coming back to the tab re-reads the settings, often in the same click that
// changes one: a read that started before a save finished can arrive after it
// and must not put the old value back. And when changes overlap, only the
// newest one decides what the control shows.
export interface SaveOrder {
  /** A save starts: returns its number. */
  saveStarted(): number
  /** A save ended, saved or not. */
  saveEnded(): void
  /** A read starts: returns its stamp. */
  readStarted(): number
  /** Whether a read with this stamp may still show what it read. */
  readCounts(stamp: number): boolean
  /** Whether the save with this number is still the newest. */
  isNewest(save: number): boolean
}

export function saveOrder(): SaveOrder {
  let saves = 0
  let running = 0
  return {
    saveStarted: () => {
      running++
      return ++saves
    },
    saveEnded: () => {
      running = Math.max(0, running - 1)
    },
    readStarted: () => saves,
    readCounts: (stamp) => running === 0 && stamp === saves,
    isNewest: (save) => save === saves,
  }
}
