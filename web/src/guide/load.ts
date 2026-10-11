import type { GuideData } from './build.ts'

// The guide's text is made while the web app is built (tools/build-guide.mjs) and
// is not part of the main script: it is fetched the first time the guide opens.
// The file may be missing in a build made without the docs folder; the guide
// then says so and points to the website.
const bundle = import.meta.glob<GuideData>('./guide.generated.json', { import: 'default' })

let loading: Promise<GuideData> | null = null

export function loadGuide(): Promise<GuideData> {
  if (!loading) {
    const load = bundle['./guide.generated.json']
    loading = load ? load() : Promise.resolve({ topics: [] })
    // A failed fetch can be tried again next time.
    loading.catch(() => {
      loading = null
    })
  }
  return loading
}
