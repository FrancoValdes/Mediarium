package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/rdborg/mediarium/internal/api"
)

func feedbackPNG(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestSendFeedbackForwardsToTheWebsite(t *testing.T) {
	var got map[string]string
	var gotShot []byte
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("relay: %v", err)
		}
		got = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			got[k] = v[0]
		}
		if fs := r.MultipartForm.File["screenshot"]; len(fs) == 1 {
			f, _ := fs[0].Open()
			var b bytes.Buffer
			_, _ = b.ReadFrom(f)
			gotShot = b.Bytes()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"id":"20261007-1"}`))
	}))
	defer relay.Close()
	defer api.SetFeedbackEndpointForTest(relay.URL)()

	_, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)

	res := postJSON[map[string]any](t, client, httpSrv.URL+"/api/feedback", map[string]any{
		"kind": "problem", "title": "Repair fails\x00", "message": "Every obfuscated release fails to repair.",
		"contact": "u/Tester", "report": "Version: 3\nlog", "lang": "en", "screenshot": feedbackPNG(t, 20, 10),
	}, http.StatusOK)
	if res["id"] != "20261007-1" {
		t.Fatalf("answer = %+v", res)
	}
	if got["title"] != "Repair fails" || got["kind"] != "problem" || got["report"] != "Version: 3\nlog" || got["contact"] != "u/Tester" || got["website"] != "" {
		t.Fatalf("relay got %+v", got)
	}
	if len(gotShot) == 0 {
		t.Fatal("the screenshot was not forwarded")
	}
	ts, _ := strconv.ParseInt(got["pow_ts"], 10, 64)
	sum := sha256.Sum256([]byte(got["title"] + "\n" + got["message"]))
	d := sha256.Sum256([]byte("mediarium-report|" + strconv.FormatInt(ts, 10) + "|" + got["pow_nonce"] + "|" + hex.EncodeToString(sum[:])))
	if api.LeadingZeroBitsForTest(d[:]) < api.FeedbackPowBits {
		t.Fatal("the stamp the website gets doesn't hold")
	}

	// Bad input never reaches the website.
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/feedback", map[string]any{"kind": "spam", "title": "x", "message": "y"}, http.StatusBadRequest)
	php := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<?php echo 1; ?>"))
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/feedback", map[string]any{"kind": "idea", "title": "An idea", "message": "Something nice to add.", "screenshot": php}, http.StatusBadRequest)

	// A few reports an hour from one server, then a pause.
	for i := 0; i < api.FeedbackPerHour-1; i++ {
		postJSON[map[string]any](t, client, httpSrv.URL+"/api/feedback", map[string]any{"kind": "idea", "title": "Idea " + strconv.Itoa(i), "message": "Something nice to add."}, http.StatusOK)
	}
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/feedback", map[string]any{"kind": "idea", "title": "One too many", "message": "Something nice to add."}, http.StatusTooManyRequests)
}

// One member using up their reports doesn't stop anyone else, least of all an
// administrator.
func TestFeedbackLimitIsPerPerson(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"id":"1"}`))
	}))
	defer relay.Close()
	defer api.SetFeedbackEndpointForTest(relay.URL)()
	e := newSignInEnv(t, nil)
	e.addAccount("kim", "member")
	kim, _, _ := e.login("kim", "long-enough-password")
	idea := map[string]any{"kind": "idea", "title": "An idea", "message": "Something nice to add."}
	for i := 0; i < api.FeedbackPerHour; i++ {
		e.must(kim, "POST", "/api/feedback", idea, http.StatusOK)
	}
	e.must(kim, "POST", "/api/feedback", idea, http.StatusTooManyRequests)
	e.must(e.admin, "POST", "/api/feedback", idea, http.StatusOK)
}
