package api

import (
	"log"
	"net/http"

	"github.com/rdborg/mediarium/internal/notify"
	"github.com/rdborg/mediarium/internal/settings"
)

// Push notifications to a browser or phone (notify/push.go): this server's
// VAPID key is made once, kept encrypted in the settings, and handed to the
// browser when it subscribes.

// pushSubject tells push services who sends the messages.
const pushSubject = "https://github.com/rdborg/Mediarium"

// setupPush loads the VAPID key, making it the first time.
func (s *Server) setupPush() {
	raw, err := s.Settings.Get(settings.KeyPushVAPID)
	if err != nil {
		log.Printf("push: read the key: %v", err)
		return
	}
	if raw != "" {
		if k, err := notify.DecodeVAPID(raw); err == nil {
			notify.SetVAPID(notify.VAPID{Private: k, Subject: pushSubject})
			return
		}
		log.Printf("push: the saved key can't be read; making a new one (devices need to allow notifications again)")
	}
	k, err := notify.NewVAPID()
	if err != nil {
		log.Printf("push: make a key: %v", err)
		return
	}
	text, err := notify.EncodeVAPID(k)
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	if err := s.Settings.Set(settings.KeyPushVAPID, text, true); err != nil {
		log.Printf("push: save the key: %v", err)
		return
	}
	notify.SetVAPID(notify.VAPID{Private: k, Subject: pushSubject})
}

// handlePushKey answers the public key a browser subscribes with.
func (s *Server) handlePushKey(w http.ResponseWriter, r *http.Request) {
	raw, err := s.Settings.Get(settings.KeyPushVAPID)
	if err != nil || raw == "" {
		writeError(w, http.StatusServiceUnavailable, "Push notifications aren't ready on this server. Restart Mediarium and try again.")
		return
	}
	k, err := notify.DecodeVAPID(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": notify.VAPID{Private: k}.PublicKey()})
}
