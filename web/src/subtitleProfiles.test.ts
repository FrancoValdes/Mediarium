// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { PROFILE_CHOICES, choiceToProfile, choicesFromSaved, profileToChoice, profilesToSave, slotName, type ProfileChoice } from './subtitleProfiles.ts'

test('every choice goes to a profile and back', () => {
  for (const c of PROFILE_CHOICES) assert.equal(profileToChoice(choiceToProfile(c)), c, c)
})

test('forced only always wants the forced track', () => {
  assert.deepEqual(choiceToProfile('forced'), { full: 'none', forced: true })
  assert.equal(profileToChoice({ full: 'none', forced: false }), 'forced')
})

test('no saved profile is the default: a normal or SDH subtitle', () => {
  assert.equal(profileToChoice(undefined), 'either')
  assert.deepEqual(choiceToProfile('either'), { full: 'either', forced: false })
  assert.equal(profileToChoice({ full: 'normal', forced: true }), 'normal+forced')
})

test('only languages with a choice other than the default are saved', () => {
  const choices: Record<string, ProfileChoice> = { en: 'normal+forced', fr: 'either', de: 'sdh' }
  assert.deepEqual(profilesToSave(choices), { en: { full: 'normal', forced: true }, de: { full: 'sdh', forced: false } })
  assert.deepEqual(profilesToSave({}), {})
  assert.deepEqual(choicesFromSaved({ en: { full: 'normal', forced: true } }), { en: 'normal+forced' })
  assert.deepEqual(choicesFromSaved(undefined), {})
})

test('a wanted forced track is named after its language', () => {
  const name = (c: string) => ({ en: 'English', 'pt-BR': 'Portuguese (Brazil)' })[c] ?? c
  assert.equal(slotName('en', name), 'English')
  assert.equal(slotName('en.forced', name), 'English (forced)')
  assert.equal(slotName('pt-BR.forced', name), 'Portuguese (Brazil) (forced)')
})
