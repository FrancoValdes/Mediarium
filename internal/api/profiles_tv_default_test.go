package api_test

import (
	"net/http"
	"strconv"
	"testing"
)

// A separate default for shows: unset it follows the movie default, set it is
// used by shows only, and it can be cleared or checked like the main default.
func TestDefaultProfileForTV(t *testing.T) {
	_, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)
	base := httpSrv.URL + "/api/quality-profiles"
	settingsURL := httpSrv.URL + "/api/settings"

	list := getJSON[map[string]any](t, client, base)
	movieDefault := int64(list["defaultId"].(float64))
	if got := list["defaultTvId"].(float64); got != 0 {
		t.Fatalf("a fresh install has no default for TV, got %v", got)
	}
	if s := getJSON[map[string]any](t, client, settingsURL); s["defaultTvProfileId"] != float64(0) {
		t.Fatalf("settings should show no TV default, got %v", s["defaultTvProfileId"])
	}
	uhd := profileIDByName(t, list, "4K & over")

	// Bad choices are refused and change nothing.
	for name, id := range map[string]int64{"unknown": 99999, "negative": -4} {
		putJSONStatus(t, client, settingsURL, map[string]any{"defaultTvProfileId": id}, http.StatusBadRequest)
		if got := getJSON[map[string]any](t, client, base)["defaultTvId"].(float64); got != 0 {
			t.Fatalf("%s: a refused choice must not be saved, got %v", name, got)
		}
	}

	// Setting it leaves the movie default alone.
	putJSONStatus(t, client, settingsURL, map[string]any{"defaultTvProfileId": uhd}, http.StatusOK)
	list = getJSON[map[string]any](t, client, base)
	if int64(list["defaultTvId"].(float64)) != uhd || int64(list["defaultId"].(float64)) != movieDefault {
		t.Fatalf("want TV default %d and movie default %d, got %+v", uhd, movieDefault, list)
	}

	// A settings update that doesn't mention it keeps it.
	putJSONStatus(t, client, settingsURL, map[string]any{"trashDays": 9}, http.StatusOK)
	if got := int64(getJSON[map[string]any](t, client, base)["defaultTvId"].(float64)); got != uhd {
		t.Fatalf("an unrelated update changed the TV default to %d", got)
	}

	// It can't be deleted while it is the default for TV.
	if code := deleteReq(t, client, base+"/"+strconv.FormatInt(uhd, 10)); code != http.StatusConflict {
		t.Fatalf("deleting the TV default should be 409, got %d", code)
	}

	// 0 clears it: shows follow the movie default again.
	putJSONStatus(t, client, settingsURL, map[string]any{"defaultTvProfileId": 0}, http.StatusOK)
	if got := getJSON[map[string]any](t, client, base)["defaultTvId"].(float64); got != 0 {
		t.Fatalf("0 should clear the TV default, got %v", got)
	}
}
