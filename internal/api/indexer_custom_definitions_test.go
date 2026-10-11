package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddingYourOwnDefinitionFile(t *testing.T) {
	_, httpSrv, client := newHuntTestServer(t)
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/onboarding/admin", map[string]string{
		"username": "ryan", "password": "correct-horse-battery-staple", "firstName": "Ryan", "lastName": "Tester",
	}, http.StatusCreated)

	raw, err := os.ReadFile(filepath.Join("..", "indexers", "testdata", "cardigann", "fixturejson.yml"))
	if err != nil {
		t.Fatal(err)
	}
	def := strings.Replace(string(raw), "id: fixturejson", "id: myhelper", 1)
	added := postJSON[map[string]any](t, client, httpSrv.URL+"/api/indexer-definitions/custom", map[string]string{"content": def}, http.StatusCreated)
	if added["id"] != "myhelper" || added["custom"] != true {
		t.Fatalf("added = %+v", added)
	}
	bad := postJSON[map[string]any](t, client, httpSrv.URL+"/api/indexer-definitions/custom", map[string]string{"content": "id: [unclosed"}, http.StatusBadRequest)
	if !strings.HasPrefix(bad["error"].(string), "This file can't be used: ") {
		t.Fatalf("bad = %+v", bad)
	}

	// The site runs on the home network, at an address the file doesn't list.
	created := postJSON[map[string]any](t, client, httpSrv.URL+"/api/indexers", map[string]any{
		"name": "My helper", "definitionId": "myhelper", "baseUrl": "http://192.168.1.20:8080/", "protocol": "torrent", "settings": map[string]any{},
	}, http.StatusCreated)
	if created["baseUrl"] != "http://192.168.1.20:8080/" {
		t.Fatalf("created = %+v", created)
	}

	// A file with the id of a definition an indexer runs on can't replace it: that would change where its saved login goes.
	postJSON[map[string]any](t, client, httpSrv.URL+"/api/indexer-definitions/custom", map[string]string{"content": def}, http.StatusConflict)

	if code := deleteReq(t, client, httpSrv.URL+"/api/indexer-definitions/custom/myhelper"); code != http.StatusConflict {
		t.Fatalf("removing a file in use: %d", code)
	}
	if code := deleteReq(t, client, httpSrv.URL+"/api/indexers/"+jsonID(int64(created["id"].(float64)))); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete indexer: %d", code)
	}
	if code := deleteReq(t, client, httpSrv.URL+"/api/indexer-definitions/custom/myhelper"); code != http.StatusNoContent {
		t.Fatalf("remove: %d", code)
	}
	if code := deleteReq(t, client, httpSrv.URL+"/api/indexer-definitions/custom/myhelper"); code != http.StatusNotFound {
		t.Fatalf("remove again: %d", code)
	}
}
