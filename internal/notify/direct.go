package notify

import "errors"

// Messages to one person (the answer to their request, to their report) go
// through the same email server and push service the administrator set up
// for notifications, but to that person's own address and devices.

// PushSenderFor returns a sender for one push subscription (the JSON a
// browser gives when it allows notifications).
func PushSenderFor(subscription string) (Sender, error) {
	return newPushSender(subscription)
}

// EmailSenderTo returns a sender that uses an email target's server and login
// but sends to one address instead of the target's own.
func EmailSenderTo(t Target, address string) (Sender, error) {
	if t.Type != TargetEmail {
		return nil, errors.New("not an email target")
	}
	cfg := make(map[string]string, len(t.Config)+1)
	for k, v := range t.Config {
		cfg[k] = v
	}
	cfg["to"] = address
	t.Config = cfg
	return NewSender(t)
}
