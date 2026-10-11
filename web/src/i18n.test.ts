// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { plural, preferredLanguage, setCatalogue, t } from './i18n.ts'

test('a stored choice wins, then the first browser language we have', () => {
  assert.equal(preferredLanguage('de', ['fr-FR']), 'de')
  assert.equal(preferredLanguage(null, ['it-IT', 'fr-CA', 'es']), 'fr')
  assert.equal(preferredLanguage('xx', ['pt-BR']), 'pt')
  assert.equal(preferredLanguage(null, ['pt-PT', 'en']), 'pt-PT')
  assert.equal(preferredLanguage(null, ['pt']), 'pt')
  assert.equal(preferredLanguage('pt-PT', ['en']), 'pt-PT')
  assert.equal(preferredLanguage(null, ['it-IT', 'nl']), 'en')
  assert.equal(preferredLanguage(null, []), 'en')
})

test('untranslated phrases fall back to the English', () => {
  setCatalogue('es', { Save: 'Guardar' })
  assert.equal(t('Save'), 'Guardar')
  assert.equal(t('Cancel'), 'Cancel')
  setCatalogue('en', {})
})

test('names in braces are filled in, unknown ones left alone', () => {
  setCatalogue('de', { '{name} was added': '{name} wurde hinzugefügt' })
  assert.equal(t('{name} was added', { name: 'Arrival' }), 'Arrival wurde hinzugefügt')
  assert.equal(t('Keep {this}', { name: 'x' }), 'Keep {this}')
  setCatalogue('en', {})
})

test('plural picks the form the language uses', () => {
  setCatalogue('en', {})
  assert.equal(plural(1, '{n} movie', '{n} movies'), '1 movie')
  assert.equal(plural(0, '{n} movie', '{n} movies'), '0 movies')
  setCatalogue('fr', { '{n} movie': '{n} film', '{n} movies': '{n} films' })
  assert.equal(plural(0, '{n} movie', '{n} movies'), '0 film')
  assert.equal(plural(2, '{n} movie', '{n} movies'), '2 films')
  setCatalogue('en', {})
})
