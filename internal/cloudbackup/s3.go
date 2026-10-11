package cloudbackup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 is S3-style object storage: Amazon S3, Backblaze B2, Wasabi,
// Cloudflare R2, MinIO and the rest that speak the same protocol. Requests
// are signed with AWS Signature Version 4.
type S3 struct {
	Endpoint  string // "https://s3.eu-central-003.backblazeb2.com"
	Region    string // "eu-central-003"; "us-east-1" when the provider doesn't care
	Bucket    string
	Prefix    string // a folder inside the bucket, like "mediarium/"
	AccessKey string
	SecretKey string
	// PathStyle puts the bucket in the path (endpoint/bucket/key) rather than
	// the host name (bucket.endpoint/key). Most providers other than Amazon
	// want it.
	PathStyle bool

	Client *http.Client
	now    func() time.Time
}

func (s *S3) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s *S3) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// objectURL is where key lives (key already includes the prefix).
func (s *S3) objectURL(key string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("the endpoint %q isn't an address", s.Endpoint)
	}
	u := *base
	if s.PathStyle {
		u.Path = "/" + s.Bucket + "/" + key
	} else {
		u.Host = s.Bucket + "." + base.Host
		u.Path = "/" + key
	}
	return &u, nil
}

// s3Escape encodes a path or query value the way Signature V4 wants:
// everything but unreserved characters, with "/" kept in paths.
func s3Escape(s string, keepSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// emptyHash is the SHA-256 of nothing, for requests without a body.
const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sign adds Signature V4 headers to req. payloadHash is the hex SHA-256 of
// the body. Every header already on the request is signed.
func (s *S3) sign(req *http.Request, payloadHash string, t time.Time) {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	// Canonical headers: host plus every header set, lower-case and sorted.
	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")

	// Canonical query: each name and value encoded, sorted by name.
	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, s3Escape(k, false)+"="+s3Escape(v, false))
		}
	}
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, s3Escape(path, true), strings.Join(parts, "&"), canonHeaders.String(), signed, payloadHash}, "\n")

	scope := day + "/" + s.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+s.SecretKey), day)
	key = hmacSHA256(key, s.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

// s3Error reads the provider's error message (XML) for a failed request.
func s3Error(resp *http.Response, what string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	_ = xml.Unmarshal(body, &e)
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	switch {
	case resp.StatusCode == http.StatusForbidden || e.Code == "SignatureDoesNotMatch" || e.Code == "InvalidAccessKeyId":
		return fmt.Errorf("%s: the storage refused the access key or secret (%s)", what, msg)
	case e.Code == "NoSuchBucket":
		return fmt.Errorf("%s: there's no bucket called that (%s)", what, msg)
	}
	return fmt.Errorf("%s: %s (HTTP %d)", what, msg, resp.StatusCode)
}

func (s *S3) key(name string) string {
	p := strings.TrimLeft(s.Prefix, "/")
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p + name
}

// Put uploads body (size bytes, whose SHA-256 is hash) as name.
func (s *S3) Put(ctx context.Context, name string, body io.Reader, size int64, hash string) error {
	u, err := s.objectURL(s.key(name))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	s.sign(req, hash, s.clock())
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return s3Error(resp, "upload "+name)
	}
	return nil
}

// List lists the backups stored under the prefix.
func (s *S3) List(ctx context.Context) ([]Object, error) {
	u, err := s.objectURL("")
	if err != nil {
		return nil, err
	}
	var out []Object
	token := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"list-type": {"2"}, "prefix": {s.key("")}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		s.sign(req, emptyHash, s.clock())
		resp, err := s.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("list the backups: %w", err)
		}
		if resp.StatusCode/100 != 2 {
			err := s3Error(resp, "list the backups")
			resp.Body.Close()
			return nil, err
		}
		var res struct {
			Contents []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&res)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read the list of backups: %w", err)
		}
		for _, c := range res.Contents {
			name := strings.TrimPrefix(c.Key, s.key(""))
			if name != "" && !strings.Contains(name, "/") {
				out = append(out, Object{Name: name, Size: c.Size, Modified: c.LastModified})
			}
		}
		if !res.Truncated || res.Next == "" {
			break
		}
		token = res.Next
	}
	return out, nil
}

// Delete removes the backup called name.
func (s *S3) Delete(ctx context.Context, name string) error {
	u, err := s.objectURL(s.key(name))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	s.sign(req, emptyHash, s.clock())
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return s3Error(resp, "delete "+name)
	}
	return nil
}
