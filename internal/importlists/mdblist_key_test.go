package importlists

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A network error never carries the MDBList key, plain or URL-encoded.
func TestMDBListErrorsHideTheKey(t *testing.T) {
	for _, key := range []string{"plainkey1234567890", "key with+special/chars&=%"} {
		c := &MDBListClient{base: "http://127.0.0.1:1", client: &http.Client{Timeout: 2 * time.Second}}
		_, err := c.Items(context.Background(), key, "someone", "a-list")
		if err == nil {
			t.Fatal("expected a connection error")
		}
		msg := err.Error()
		for _, form := range []string{key, strings.ReplaceAll(key, " ", "+"), strings.ReplaceAll(key, " ", "%20")} {
			if strings.Contains(msg, form) {
				t.Errorf("the key %q is in the error: %s", form, msg)
			}
		}
		if strings.Contains(msg, "%2B") || strings.Contains(msg, "%26") {
			t.Errorf("an encoded key is in the error: %s", msg)
		}
	}
}
