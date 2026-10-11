package cloudbackup

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// WebDAV is a folder on a WebDAV server: Nextcloud, ownCloud, many NAS
// boxes (Synology WebDAV Server, QNAP), or anything served by rclone.
type WebDAV struct {
	URL      string // the folder: "https://cloud.example.com/remote.php/dav/files/ryan/Mediarium/"
	Username string
	Password string

	Client *http.Client
}

func (d *WebDAV) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return http.DefaultClient
}

func (d *WebDAV) folder() (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(d.URL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("the folder %q isn't a web address", d.URL)
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u, nil
}

func (d *WebDAV) do(ctx context.Context, method, target string, body io.Reader, size int64, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if d.Username != "" || d.Password != "" {
		req.SetBasicAuth(d.Username, d.Password)
	}
	return d.client().Do(req)
}

func davError(resp *http.Response, what string) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: the server refused the user name or password (HTTP %d)", what, resp.StatusCode)
	case http.StatusNotFound, http.StatusConflict:
		return fmt.Errorf("%s: the folder doesn't exist on the server (HTTP %d)", what, resp.StatusCode)
	case http.StatusInsufficientStorage:
		return fmt.Errorf("%s: the server is out of space", what)
	}
	return fmt.Errorf("%s: HTTP %d", what, resp.StatusCode)
}

// ensureFolder creates the folder when it isn't there yet (only the last
// part: the folders above it must exist).
func (d *WebDAV) ensureFolder(ctx context.Context, u *url.URL) error {
	resp, err := d.do(ctx, "MKCOL", u.String(), nil, 0, nil)
	if err != nil {
		return fmt.Errorf("create the folder: %w", err)
	}
	resp.Body.Close()
	// 201 made it; 405 means it was there already.
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode/100 == 2 {
		return nil
	}
	return davError(resp, "create the folder")
}

func (d *WebDAV) Put(ctx context.Context, name string, body io.Reader, size int64, _ string) error {
	u, err := d.folder()
	if err != nil {
		return err
	}
	if err := d.ensureFolder(ctx, u); err != nil {
		return err
	}
	target := u.JoinPath(name)
	resp, err := d.do(ctx, http.MethodPut, target.String(), body, size, map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return davError(resp, "upload "+name)
	}
	return nil
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:getcontentlength/><d:getlastmodified/><d:resourcetype/></d:prop></d:propfind>`

func (d *WebDAV) List(ctx context.Context) ([]Object, error) {
	u, err := d.folder()
	if err != nil {
		return nil, err
	}
	resp, err := d.do(ctx, "PROPFIND", u.String(), strings.NewReader(propfindBody), int64(len(propfindBody)), map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if err != nil {
		return nil, fmt.Errorf("list the backups: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no folder yet, so no backups
	}
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode/100 != 2 {
		return nil, davError(resp, "list the backups")
	}
	var ms struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Length   int64  `xml:"prop>getcontentlength"`
				Modified string `xml:"prop>getlastmodified"`
				Type     struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"prop>resourcetype"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("read the list of backups: %w", err)
	}
	var out []Object
	for _, r := range ms.Responses {
		p, err := url.PathUnescape(r.Href)
		if err != nil {
			p = r.Href
		}
		if strings.HasSuffix(p, "/") {
			continue // the folder itself, or a folder inside it
		}
		o := Object{Name: path.Base(p)}
		folder := false
		for _, ps := range r.Props {
			if ps.Type.Collection != nil {
				folder = true
			}
			if ps.Length > 0 {
				o.Size = ps.Length
			}
			if t, err := http.ParseTime(ps.Modified); err == nil {
				o.Modified = t
			}
		}
		if !folder {
			out = append(out, o)
		}
	}
	return out, nil
}

func (d *WebDAV) Delete(ctx context.Context, name string) error {
	u, err := d.folder()
	if err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodDelete, u.JoinPath(name).String(), nil, 0, nil)
	if err != nil {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return davError(resp, "delete "+name)
	}
	return nil
}
