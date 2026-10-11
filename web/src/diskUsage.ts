// The "Also delete everything on disk" choice when a title is removed. It is
// off unless the person ticks it, and it says how much would go.
import type { DiskUsage } from './api'
import type { ConfirmOptions } from './components/ConfirmProvider'
import { formatBytes } from './format.ts'
import { plural, t } from './i18n.ts'

// "3 files, 11.0 GB", or a plain line when there is nothing on disk.
export function diskSummary(u: DiskUsage | null): string {
  if (!u) return ''
  if (u.files === 0) return t('no files')
  return plural(u.files, '{n} file, {size}', '{n} files, {size}', { size: formatBytes(u.bytes) })
}

// The sentences that name what is removed. Each kind has its own, so every
// language can say "this movie" / "this show" its own way. Anything that isn't
// a show or an artist reads as a movie.
function kindLines(noun: string, days: number): { none: string; toBin: string; gone: string } {
  if (noun === 'artist') {
    return {
      none: t('There are no files in your library for this artist.'),
      toBin: plural(days, 'The files of this artist go to the recycle bin (Activity > Recycle bin) and are deleted for good after {n} day.', 'The files of this artist go to the recycle bin (Activity > Recycle bin) and are deleted for good after {n} days.'),
      gone: t('The files of this artist will be deleted for good. There is no undo.'),
    }
  }
  if (noun === 'show') {
    return {
      none: t('There are no files in your library for this show.'),
      toBin: plural(days, 'The files of this show go to the recycle bin (Activity > Recycle bin) and are deleted for good after {n} day.', 'The files of this show go to the recycle bin (Activity > Recycle bin) and are deleted for good after {n} days.'),
      gone: t('The files of this show will be deleted for good. There is no undo.'),
    }
  }
  return {
    none: t('There are no files in your library for this movie.'),
    toBin: plural(days, 'The files of this movie go to the recycle bin (Activity > Recycle bin) and are deleted for good after {n} day.', 'The files of this movie go to the recycle bin (Activity > Recycle bin) and are deleted for good after {n} days.'),
    gone: t('The files of this movie will be deleted for good. There is no undo.'),
  }
}

// The tick box for the remove question. noun is "movie", "show" or "artist".
// It starts unticked, says how much would go, and shows a red warning while it
// is ticked.
export function removeOption(noun: string, u: DiskUsage | null): NonNullable<ConfirmOptions['option']> {
  const what = noun === 'artist' ? t("The artist's albums in your music folder") : t('The files in your library')
  const days = u?.trashDays ?? 0
  const lines = kindLines(noun, days)
  const after = days > 0 ? plural(days, 'They wait in the recycle bin for {n} day first.', 'They wait in the recycle bin for {n} days first.') : t('This cannot be undone.')
  let hint: string
  if (u && u.files === 0) hint = lines.none
  else if (u) hint = t('{what}: {summary}. {after}', { what, summary: diskSummary(u), after })
  else hint = `${what}. ${after}`
  return {
    label: t('Also delete everything on disk'),
    hint,
    defaultChecked: false,
    warning: days > 0 ? lines.toBin : lines.gone,
  }
}
