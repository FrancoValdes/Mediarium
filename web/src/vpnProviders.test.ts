// Run with: npm test
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import { confHints, OTHER_PROVIDER, providerName, vpnProviders } from './vpnProviders.ts'
import { hostPort, ipWithPrefix, wireguardKey } from './validate.ts'
import { parseWireGuardConf } from './wgConf.ts'

// The sample configs are shared with the Go tests (internal/vpn and
// internal/api): fake keys, in the shape each provider documents.
const SAMPLES = new URL('../../internal/vpn/testdata/providers/', import.meta.url)
const samples = readdirSync(SAMPLES)
  .filter((f) => f.endsWith('.conf'))
  .map((f) => [f.replace(/\.conf$/, ''), readFileSync(new URL(f, SAMPLES), 'utf8')] as const)

test('the provider list is alphabetical, with "not listed" last', () => {
  const list = vpnProviders()
  assert.equal(list[list.length - 1].id, OTHER_PROVIDER)
  const names = list.slice(0, -1).map((p) => p.name.toLowerCase())
  assert.deepEqual(names, [...names].sort((a, b) => a.localeCompare(b)))
})

test('provider ids are unique, and the ids saved by earlier versions are still there', () => {
  const ids = vpnProviders().map((p) => p.id)
  assert.equal(new Set(ids).size, ids.length)
  for (const old of ['mullvad', 'protonvpn', 'pia', 'surfshark', 'nordvpn', 'windscribe', 'custom']) assert.ok(ids.includes(old), old)
  assert.equal(providerName('custom'), 'Not listed / any other provider')
  assert.equal(providerName('some-old-id'), 'some-old-id')
})

test('every provider has steps, a plain https link and no promotion', () => {
  for (const p of vpnProviders()) {
    assert.ok(p.steps.length >= 2, `${p.id} steps`)
    if (p.link) {
      const url = new URL(p.link)
      assert.equal(url.protocol, 'https:', `${p.id} link`)
      assert.equal(url.search, '', `${p.id} link has a query string`)
    }
    const text = [p.name, ...p.steps, ...p.notes].join(' ')
    assert.doesNotMatch(text, /\b(best|fastest|top|seamless|leading|cheapest|#1)\b/i, `${p.id} promotes`)
    assert.doesNotMatch(text, /[{}]/, `${p.id} has a stray brace`)
  }
})

test('preset values are only ones that are the same for every customer, and are valid', () => {
  for (const p of vpnProviders()) {
    for (const d of p.dns) {
      assert.equal(ipWithPrefix(d), null, `${p.id} dns ${d}`)
      assert.ok(!d.includes('/'), `${p.id} dns ${d}`)
    }
  }
})

test('every provider with a sample file has one, and the list of samples has no orphans', () => {
  const sampleOf: Record<string, string[]> = { selfhosted: ['pivpn', 'wgeasy'], [OTHER_PROVIDER]: [] }
  const have = new Set(samples.map(([n]) => n))
  const used = new Set<string>()
  for (const p of vpnProviders()) {
    for (const s of sampleOf[p.id] ?? [p.id]) {
      assert.ok(have.has(s), `${p.id} needs testdata/providers/${s}.conf`)
      used.add(s)
    }
  }
  for (const n of have) assert.ok(used.has(n), `testdata/providers/${n}.conf belongs to no provider`)
})

// What the page does with a pasted file: read it, then check each field the way
// the form does. A real file from any provider must come through with no errors.
for (const [name, text] of samples) {
  test(`the ${name} sample fills in the form with no errors`, () => {
    const c = parseWireGuardConf(text)
    assert.ok(c, 'parsed')
    assert.equal(wireguardKey(c.privateKey, 'key'), null)
    assert.equal(c.privateKey.length, 44)
    assert.equal(wireguardKey(c.peerPublicKey, 'key'), null)
    assert.equal(c.peerPublicKey.length, 44)
    assert.equal(wireguardKey(c.presharedKey, 'key'), null)
    assert.equal(hostPort(c.endpoint), null, c.endpoint)
    assert.ok(c.endpoint !== '')
    assert.ok(c.localAddresses.length > 0)
    for (const a of c.localAddresses) assert.equal(ipWithPrefix(a), null, a)
    for (const d of c.dns) assert.equal(ipWithPrefix(d), null, d)
    for (const a of c.allowedIps) assert.equal(ipWithPrefix(a, '0.0.0.0/0'), null, a)
  })
}

test('details of the sample files that the form has to get right', () => {
  const get = (n: string) => {
    const c = parseWireGuardConf(samples.find(([s]) => s === n)![1])
    assert.ok(c)
    return c
  }
  // Mullvad writes its lists without spaces and ::0/0.
  const mullvad = get('mullvad')
  assert.deepEqual(mullvad.localAddresses, ['10.66.12.34/32', 'fc00:bbbb:bbbb:bb01::3:c21/128'])
  assert.deepEqual(mullvad.allowedIps, ['0.0.0.0/0', '::0/0'])
  assert.deepEqual(mullvad.dns, ['10.64.0.1'])
  // PIA writes its address with no /prefix, and puts [Peer] lines in any order.
  assert.deepEqual(get('pia').localAddresses, ['10.18.44.12'])
  // Surfshark's address has a /16, and the endpoint is a host name.
  const surfshark = get('surfshark')
  assert.deepEqual(surfshark.localAddresses, ['10.14.0.2/16'])
  assert.equal(surfshark.endpoint, 'us-slc.prod.surfshark.com:51820')
  assert.deepEqual(surfshark.dns, vpnProviders().find((p) => p.id === 'surfshark')!.dns)
  // Hook lines (wg-easy) are left alone, and the preshared key comes through.
  const wgEasy = get('wgeasy')
  assert.ok(wgEasy.presharedKey)
  assert.deepEqual(wgEasy.dns, ['1.1.1.1', '2606:4700:4700::1111'])
  // IPVanish leaves the preshared key empty.
  assert.equal(get('ipvanish').presharedKey, '')
  // Windscribe needs its preshared key; the others don't.
  for (const p of vpnProviders()) {
    const have = samples.find(([s]) => s === p.id)
    if (!have) continue
    const hints = confHints(p, parseWireGuardConf(have[1])!)
    assert.deepEqual(hints, [], p.id)
  }
})

test('a file with a missing key gets a hint', () => {
  const windscribe = vpnProviders().find((p) => p.id === 'windscribe')!
  const text = samples.find(([s]) => s === 'windscribe')![1].replace(/^PresharedKey.*$/m, '')
  const hints = confHints(windscribe, parseWireGuardConf(text)!)
  assert.equal(hints.length, 1)
  assert.match(hints[0], /preshared key/)
  const noPrivate = parseWireGuardConf('[Interface]\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = x\nEndpoint = a.b:1')!
  assert.match(confHints(vpnProviders()[0], noPrivate)[0], /private key/)
})
