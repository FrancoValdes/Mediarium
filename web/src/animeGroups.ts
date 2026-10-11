// A starting point for the preferred terms of an anime profile. For anime the
// release group matters more than the source: the same episode comes out
// within hours from several groups, and they differ in speed, in encodes and in
// which languages they keep. The scores are only a start; the numbers are the
// person's to change.
export const ANIME_GROUP_TERMS: { term: string; score: number }[] = [
  { term: 'SubsPlease', score: 40 }, // simulcasts, out within minutes of airing
  { term: 'EMBER', score: 30 }, // careful encodes of web and disc releases
  { term: 'Judas', score: 30 }, // small files, several audio and subtitle tracks
  { term: 'Erai-raws', score: 20 }, // simulcasts with many subtitle languages
  { term: 'Dual Audio', score: 20 }, // the Japanese and the English audio together
]

const termOf = (line: string) => {
  const i = line.lastIndexOf('=')
  return (i < 0 ? line : line.slice(0, i)).trim().toLowerCase()
}

// Adds the anime lines to the preferred terms text, leaving out a term that is
// already there (whatever its score), so pressing the button twice or after
// editing changes nothing it should not.
export function addAnimeTerms(text: string): string {
  const lines = text.split('\n').map((l) => l.trim()).filter(Boolean)
  const have = new Set(lines.map(termOf))
  for (const { term, score } of ANIME_GROUP_TERMS) {
    if (!have.has(term.toLowerCase())) lines.push(`${term} = ${score}`)
  }
  return lines.join('\n')
}
