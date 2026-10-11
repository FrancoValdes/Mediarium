package notify

import (
	"testing"
)

func TestEmailSenderToUsesTheTargetsServerButOtherAddress(t *testing.T) {
	target := Target{Type: TargetEmail, Name: "Mail", Enabled: true, Config: map[string]string{
		"host": "smtp.example.com", "port": "587", "security": "starttls", "username": "me", "password": "secret",
		"from": "me@example.com", "to": "admin@example.com",
	}}
	s, err := EmailSenderTo(target, "sam@example.com")
	if err != nil {
		t.Fatal(err)
	}
	es, ok := s.(*EmailSender)
	if !ok {
		t.Fatalf("sender is %T", s)
	}
	if es.Host != "smtp.example.com" || es.Port != 587 || es.Username != "me" || es.Password != "secret" || es.From != "me@example.com" {
		t.Fatalf("server settings not kept: %+v", es)
	}
	if len(es.To) != 1 || es.To[0] != "sam@example.com" {
		t.Fatalf("recipients: %v", es.To)
	}
	if target.Config["to"] != "admin@example.com" {
		t.Fatal("the target's own address was changed")
	}
	if _, err := EmailSenderTo(Target{Type: TargetDiscord}, "sam@example.com"); err == nil {
		t.Fatal("a Discord target was used for email")
	}
}

func TestPushSenderForRefusesAnUnusableSubscription(t *testing.T) {
	if _, err := PushSenderFor("nonsense"); err == nil {
		t.Fatal("a subscription that isn't JSON was accepted")
	}
	if _, err := PushSenderFor(`{"endpoint":"http://push.example/a","keys":{}}`); err == nil {
		t.Fatal("a subscription that isn't https was accepted")
	}
}
