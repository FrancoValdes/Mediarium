// Titles sort without a leading "The", "A" or "An": The Matrix goes under M.

const LEADING = /^(the|an|a)\s+/i

/** titleSortKey is the title as it should be sorted: no leading article, no leading quote or bracket. */
export function titleSortKey(title: string): string {
  const plain = title.trim().replace(/^["'“‘([]+/, '')
  const rest = plain.replace(LEADING, '')
  return rest === '' ? plain : rest
}

/** compareTitles orders two titles the way the library lists them. */
export function compareTitles(a: string, b: string): number {
  return titleSortKey(a).localeCompare(titleSortKey(b), undefined, { sensitivity: 'base', numeric: true }) || a.localeCompare(b)
}
