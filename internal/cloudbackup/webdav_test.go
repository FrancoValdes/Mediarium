package cloudbackup

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A list reply in the shape Nextcloud sends: the folder itself, a sub-folder,
// two files, and a second propstat holding the properties it doesn't know.
const nextcloudList = `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">
 <d:response><d:href>/remote.php/dav/files/ryan/Mediarium/</d:href>
  <d:propstat><d:prop><d:getlastmodified>Mon, 05 Oct 2026 03:00:00 GMT</d:getlastmodified><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
 </d:response>
 <d:response><d:href>/remote.php/dav/files/ryan/Mediarium/old/</d:href>
  <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
 </d:response>
 <d:response><d:href>/remote.php/dav/files/ryan/Mediarium/mediarium-backup-2026-10-05-0300.zip.enc</d:href>
  <d:propstat><d:prop><d:getcontentlength>1234</d:getcontentlength><d:getlastmodified>Mon, 05 Oct 2026 03:00:00 GMT</d:getlastmodified><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  <d:propstat><d:prop><oc:size/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>
 </d:response>
 <d:response><d:href>/remote.php/dav/files/ryan/Mediarium/my%20backup.zip.enc</d:href>
  <d:propstat><d:prop><d:getcontentlength>99</d:getcontentlength><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
 </d:response>
</d:multistatus>`

func TestWebDAV(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "ryan" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		got = append(got, r.Method+" "+r.URL.Path)
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusMethodNotAllowed) // already there
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			if string(b) != "locked bytes" {
				t.Errorf("uploaded %q", b)
			}
			w.WriteHeader(http.StatusCreated)
		case "PROPFIND":
			if r.Header.Get("Depth") != "1" {
				t.Errorf("Depth %q", r.Header.Get("Depth"))
			}
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, nextcloudList)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	d := &WebDAV{URL: srv.URL + "/remote.php/dav/files/ryan/Mediarium", Username: "ryan", Password: "secret"}

	if err := d.Put(ctx, "b.zip.enc", strings.NewReader("locked bytes"), 12, ""); err != nil {
		t.Fatal(err)
	}
	list, err := d.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "mediarium-backup-2026-10-05-0300.zip.enc" || list[0].Size != 1234 || list[0].Modified.IsZero() ||
		list[1].Name != "my backup.zip.enc" || list[1].Size != 99 {
		t.Fatalf("list: %+v", list)
	}
	if err := d.Delete(ctx, "b.zip.enc"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"MKCOL /remote.php/dav/files/ryan/Mediarium/",
		"PUT /remote.php/dav/files/ryan/Mediarium/b.zip.enc",
		"PROPFIND /remote.php/dav/files/ryan/Mediarium/",
		"DELETE /remote.php/dav/files/ryan/Mediarium/b.zip.enc",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s", strings.Join(got, "\n"))
	}

	bad := &WebDAV{URL: d.URL, Username: "ryan", Password: "wrong"}
	if _, err := bad.List(ctx); err == nil || !strings.Contains(err.Error(), "user name or password") {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := (&WebDAV{URL: "ftp://example.com/x"}).List(ctx); err == nil {
		t.Fatal("an ftp address was accepted")
	}
}
