package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // screenshots
	_ "image/png"  // screenshots
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rdborg/mediarium/internal/netproxy"
)

// Problem reports and ideas sent from inside the app ("Send privately").
// The browser hands the report here; the server checks it, adds a
// proof-of-work stamp and forwards it to the project's website, which keeps
// it private and emails the developer. The other way, "Open on GitHub",
// happens entirely in the browser.

// feedbackEndpoint is where private reports go. A variable so tests can
// point it at a local server.
var feedbackEndpoint = "https://mediarium.app/api/report.php"

const (
	feedbackPowBits        = 20
	feedbackMaxScreenshot  = 5 << 20
	feedbackMaxImageSide   = 8000
	feedbackPerHour        = 5
	feedbackMaxReportBytes = 200_000
)

type feedbackRequest struct {
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	Contact    string `json:"contact"`
	Report     string `json:"report"`
	Lang       string `json:"lang"`
	Screenshot string `json:"screenshot"` // a data: URL of a PNG or JPEG, optional
}

var feedbackLimit struct {
	sync.Mutex
	sent []time.Time
}

// feedbackAllowed keeps one install from sending more than a few reports an
// hour, whoever is signed in.
func feedbackAllowed(now time.Time) bool {
	feedbackLimit.Lock()
	defer feedbackLimit.Unlock()
	kept := feedbackLimit.sent[:0]
	for _, t := range feedbackLimit.sent {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	feedbackLimit.sent = kept
	if len(kept) >= feedbackPerHour {
		return false
	}
	feedbackLimit.sent = append(feedbackLimit.sent, now)
	return true
}

// powStamp finds a nonce whose stamp hash starts with bits zero bits. About
// a million hashes for 20 bits: a fraction of a second here, and too costly
// to send reports by the thousand.
func powStamp(ctx context.Context, ts int64, title, message string, bits int) (string, error) {
	sum := sha256.Sum256([]byte(title + "\n" + message))
	prefix := "mediarium-report|" + strconv.FormatInt(ts, 10) + "|"
	suffix := "|" + hex.EncodeToString(sum[:])
	for n := uint64(0); ; n++ {
		if n&0xffff == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		nonce := strconv.FormatUint(n, 16)
		d := sha256.Sum256([]byte(prefix + nonce + suffix))
		if leadingZeroBits(d[:]) >= bits {
			return nonce, nil
		}
	}
}

func leadingZeroBits(b []byte) int {
	n := 0
	for _, c := range b {
		if c == 0 {
			n += 8
			continue
		}
		for bit := 7; bit >= 0 && c&(1<<bit) == 0; bit-- {
			n++
		}
		break
	}
	return n
}

// decodeScreenshot checks a data: URL holds a real PNG or JPEG of sensible
// size and returns its bytes and type.
func decodeScreenshot(dataURL string) ([]byte, string, error) {
	if dataURL == "" {
		return nil, "", nil
	}
	head, b64, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, "", errors.New("the screenshot couldn't be read")
	}
	var ctype string
	switch head {
	case "data:image/png;base64":
		ctype = "image/png"
	case "data:image/jpeg;base64":
		ctype = "image/jpeg"
	default:
		return nil, "", errors.New("the screenshot must be a PNG or JPEG image")
	}
	if base64.StdEncoding.DecodedLen(len(b64)) > feedbackMaxScreenshot {
		return nil, "", errors.New("the screenshot is too large")
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", errors.New("the screenshot couldn't be read")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || "image/"+format != ctype {
		return nil, "", errors.New("the screenshot must be a PNG or JPEG image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > feedbackMaxImageSide || cfg.Height > feedbackMaxImageSide {
		return nil, "", errors.New("the screenshot is too large")
	}
	return data, ctype, nil
}

func cleanLine(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func cleanText(s string, maxBytes int) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\r\n", "\n"), "")
	s = strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxBytes {
		s = strings.ToValidUTF8(s[:maxBytes], "")
	}
	return s
}

// handleSendFeedback forwards a report or idea to the developer.
func (s *Server) handleSendFeedback(w http.ResponseWriter, r *http.Request) {
	var req feedbackRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that report. Reload the page and try again.")
		return
	}
	if req.Kind != "problem" && req.Kind != "idea" {
		writeError(w, http.StatusBadRequest, "Choose whether this is a problem or an idea.")
		return
	}
	title := cleanLine(req.Title, 150)
	message := cleanText(req.Message, 8000)
	if utf8.RuneCountInString(title) < 3 {
		writeError(w, http.StatusBadRequest, "Give it a short title.")
		return
	}
	if utf8.RuneCountInString(message) < 10 {
		writeError(w, http.StatusBadRequest, "Describe what happened or what you'd like in a sentence or two.")
		return
	}
	shot, shotType, err := decodeScreenshot(req.Screenshot)
	if err != nil {
		writeError(w, http.StatusBadRequest, strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
		return
	}
	// Only an administrator can read the support report, so only theirs goes along.
	report := ""
	if isAdminRequest(r) {
		report = cleanText(req.Report, feedbackMaxReportBytes)
	}
	if !feedbackAllowed(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "A few reports were sent from this server in the last hour. Please wait a while, or open it on GitHub instead.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	ts := time.Now().Unix()
	nonce, err := powStamp(ctx, ts, title, message, feedbackPowBits)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Couldn't send the report. Please try again.")
		return
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	lang := req.Lang
	if len(lang) > 10 {
		lang = ""
	}
	for _, f := range [][2]string{
		{"kind", req.Kind}, {"title", title}, {"message", message}, {"contact", cleanLine(req.Contact, 200)},
		{"report", report}, {"version", s.version}, {"lang", lang},
		{"pow_ts", strconv.FormatInt(ts, 10)}, {"pow_nonce", nonce}, {"website", ""},
	} {
		_ = mw.WriteField(f[0], f[1])
	}
	if shot != nil {
		ext := ".png"
		if shotType == "image/jpeg" {
			ext = ".jpg"
		}
		part, err := mw.CreatePart(map[string][]string{
			"Content-Disposition": {`form-data; name="screenshot"; filename="screenshot` + ext + `"`},
			"Content-Type":        {shotType},
		})
		if err == nil {
			_, _ = part.Write(shot)
		}
	}
	_ = mw.Close()

	post, err := http.NewRequestWithContext(ctx, http.MethodPost, feedbackEndpoint, &body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't send the report. Please try again.")
		return
	}
	post.Header.Set("Content-Type", mw.FormDataContentType())
	post.Header.Set("User-Agent", "Mediarium/"+s.version)
	resp, err := netproxy.Client(40*time.Second, netproxy.Indexers).Do(post)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Couldn't reach mediarium.app to send the report. Check this server's internet connection, or open it on GitHub instead.")
		return
	}
	defer resp.Body.Close()
	var answer struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)
	if resp.StatusCode != http.StatusOK || !answer.OK {
		msg := cleanLine(answer.Error, 300)
		if msg == "" {
			msg = fmt.Sprintf("The report wasn't accepted (error %d). Please try again later, or open it on GitHub instead.", resp.StatusCode)
		}
		status := http.StatusBadGateway
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadRequest {
			status = resp.StatusCode
		}
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": cleanLine(answer.ID, 64)})
}
