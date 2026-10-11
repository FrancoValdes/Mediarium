// Lists the phrases the app shows that a language doesn't translate yet, and
// the catalogue entries no longer used. Phrases are found by reading every
// t('...'), tx('...') and plural(n, '...', '...') call in src.
//
//   node tools/i18n-check.mjs           report, always exits 0
//   node tools/i18n-check.mjs --strict  exit 1 when anything is missing or a call isn't a plain string
//   node tools/i18n-check.mjs --prune   also drop unused entries from the catalogues
//   node tools/i18n-check.mjs --list    print every phrase as JSON
import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const src = join(root, 'src')
const args = new Set(process.argv.slice(2))
const LANGS = ['es', 'de', 'fr', 'pt', 'pt-PT']

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return name === 'locales' ? [] : files(p)
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) && name !== 'i18n.ts' ? [p] : []
  })
}

const LIT = String.raw`'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"`
const unquote = (lit) => lit.slice(1, -1).replace(/\\(.)/g, (_, c) => ({ n: '\n', t: '\t' })[c] ?? c)

const phrases = new Map() // phrase -> first place it's used
const bad = []
for (const file of files(src)) {
  const text = readFileSync(file, 'utf8')
  const where = (i) => `${relative(root, file)}:${text.slice(0, i).split('\n').length}`
  const add = (lit, i) => {
    const p = unquote(lit)
    if (!phrases.has(p)) phrases.set(p, where(i))
  }
  for (const m of text.matchAll(new RegExp(String.raw`(?<![\w.$])(t|tx)\(\s*(${LIT}|[^)\s])`, 'g'))) {
    if (/^['"]/.test(m[2])) add(m[2], m.index)
    else bad.push(`${where(m.index)} ${m[1]}() needs a plain string`)
  }
  for (const m of text.matchAll(new RegExp(String.raw`(?<![\w.$])plural\(\s*([^,'"\n]+),\s*(${LIT}|[^,]+),\s*(${LIT}|[^,)]+)`, 'g'))) {
    for (const lit of [m[2], m[3]]) {
      if (/^['"]/.test(lit)) add(lit, m.index)
      else bad.push(`${where(m.index)} plural() needs plain strings`)
    }
  }
  // tc('context', 'Text') has its own entry, "context|Text".
  for (const m of text.matchAll(new RegExp(String.raw`(?<![\w.$])tc\(\s*(${LIT}|[^,]+),\s*(${LIT}|[^,)]+)`, 'g'))) {
    if (/^['"]/.test(m[1]) && /^['"]/.test(m[2])) {
      const key = unquote(m[1]) + '|' + unquote(m[2])
      if (!phrases.has(key)) phrases.set(key, where(m.index))
    } else bad.push(`${where(m.index)} tc() needs plain strings`)
  }
}

if (args.has('--list')) {
  console.log(JSON.stringify([...phrases.keys()], null, 2))
  process.exit(0)
}

let problems = bad.length
for (const b of bad) console.log(b)
for (const lang of LANGS) {
  const path = join(src, 'locales', `${lang}.json`)
  const cat = JSON.parse(readFileSync(path, 'utf8'))
  const missing = [...phrases.keys()].filter((p) => !cat[p])
  const unused = Object.keys(cat).filter((p) => !phrases.has(p))
  console.log(`${lang}: ${phrases.size - missing.length}/${phrases.size} translated, ${unused.length} unused`)
  for (const p of missing.slice(0, args.has('--strict') ? 20 : 5)) console.log(`  missing: ${JSON.stringify(p)} (${phrases.get(p)})`)
  problems += missing.length
  if (args.has('--prune') && unused.length) {
    for (const p of unused) delete cat[p]
    writeFileSync(path, JSON.stringify(cat, null, 2) + '\n')
  }
}
if (args.has('--strict') && problems > 0) process.exit(1)
