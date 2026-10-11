import { t } from './i18n.ts'

// What the sign-in page says when single sign-on sent the browser back with a
// problem code (?signin_error=...). The server only ever sends one of these
// short codes, never text from the provider.
export function signInProblem(code: string, name: string): string {
  switch (code) {
    case 'off':
      return t("Single sign-on isn't switched on.")
    case 'no_account':
      return t("You signed in at {name}, but there's no account for you here yet. Ask an administrator to add one.", { name })
    case 'not_allowed':
      return t("That account isn't allowed to sign in here.")
    case 'disabled':
      return t('This account is turned off. Ask an administrator to turn it on again.')
    case 'email_unverified':
      return t("{name} hasn't confirmed your email address, so it can't be matched to an account here.", { name })
    case 'link_admin':
      return t('An administrator account can only be linked from its own profile. Sign in with your password, then link it there.')
    case 'ambiguous':
      return t('More than one account here has your email address. Ask an administrator.')
    case 'linked_already':
      return t('That sign-in is already linked to another account.')
    case 'state':
      return t('That sign-in took too long, or was started in another browser. Try again.')
    case 'limit':
      return t('Too many tries. Wait a few minutes and try again.')
  }
  return t("Signing in with {name} didn't work. Try again, and tell an administrator if it keeps happening.", { name })
}
