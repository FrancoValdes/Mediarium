package api

import (
	"net"
	"net/url"
	"strings"
)

// A saved password, key or token belongs to the address it was saved with.
// "Test" and "Save" let a form leave the secret blank to keep the saved one;
// if the address could be changed in the same request, an account that can
// change settings (or an API key stolen from one) could point the server at a
// machine of its own and have the saved secret sent there. So when the
// address changes, the secret has to be typed again.

// addrKey reduces an address (a URL, or a host name with or without a port) to
// its lower-case host. The port is left out on purpose: another port on the
// same machine is still the same machine, and changing it (say from the
// unencrypted port to the encrypted one) is an ordinary edit.
func addrKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		}
		return strings.ToLower(raw)
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		raw = h
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(raw, "[]"), "."))
}

// addrMoved reports whether two addresses lead to different places, or the
// same place without encryption: a secret saved for https:// must not go out
// in clear text because the address was changed to http://.
func addrMoved(before, after string) bool {
	return addrKey(before) != addrKey(after) || (secureScheme(before) && insecureScheme(after))
}

func addrScheme(raw string) string {
	if i := strings.Index(strings.TrimSpace(raw), "://"); i > 0 {
		return strings.ToLower(strings.TrimSpace(raw)[:i])
	}
	return ""
}

func secureScheme(raw string) bool {
	s := addrScheme(raw)
	return s == "https" || s == "wss"
}

func insecureScheme(raw string) bool {
	s := addrScheme(raw)
	return s == "http" || s == "ws"
}

// retypeSecretMessage is the answer when an address changed but the secret
// that was saved for the old one was left blank.
func retypeSecretMessage(what string) string {
	return "You changed the address, so type the " + what + " again. A saved " + what + " is only ever sent to the address it was saved for."
}
