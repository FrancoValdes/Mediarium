package animemap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rdborg/mediarium/internal/netguard"
)

const (
	aniListURL     = "https://graphql.anilist.co"
	aniListPerPage = 50
	maxTitles      = 12
	maxAniList     = 4 << 20
)

// AniList gives the names an anime is known by. One request covers up to 50
// of them. AniList allows 90 requests a minute (30 while it is degraded, as it
// has been for a while); a refresh makes one, spaced from the last.
type AniList struct {
	url       string
	userAgent string
	http      *http.Client
	gate      gate
}

func NewAniList(userAgent string) *AniList {
	return &AniList{url: aniListURL, userAgent: userAgent, http: netguard.Client(30 * time.Second), gate: gate{gap: 2 * time.Second}}
}

// SetURL points the client at a test server.
func (a *AniList) SetURL(u string) {
	a.url = u
	a.gate.gap = 0
}

const aniListQuery = `query ($ids: [Int], $perPage: Int) { Page(page: 1, perPage: $perPage) { media(id_in: $ids, type: ANIME) { id idMal title { romaji english native } synonyms } } }`

type aniListAnswer struct {
	Data struct {
		Page struct {
			Media []struct {
				ID    int `json:"id"`
				IDMal int `json:"idMal"`
				Title struct {
					Romaji  string `json:"romaji"`
					English string `json:"english"`
					Native  string `json:"native"`
				} `json:"title"`
				Synonyms []string `json:"synonyms"`
			} `json:"media"`
		} `json:"Page"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Titles returns the names of each anime by its AniList id. An id AniList
// doesn't know has no entry in the answer.
func (a *AniList) Titles(ctx context.Context, ids []int) (map[int][]string, error) {
	out := map[int][]string{}
	for start := 0; start < len(ids); start += aniListPerPage {
		chunk := ids[start:min(start+aniListPerPage, len(ids))]
		part, err := a.page(ctx, chunk)
		if err != nil {
			return out, err
		}
		for id, t := range part {
			out[id] = t
		}
	}
	return out, nil
}

func (a *AniList) page(ctx context.Context, ids []int) (map[int][]string, error) {
	if err := a.gate.wait(ctx); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"query": aniListQuery, "variables": map[string]any{"ids": ids, "perPage": aniListPerPage}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return nil, netguard.CleanError(err)
	}
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: AniList: %v", ErrOffline, netguard.CleanError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := ""
		if s := resp.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				wait = fmt.Sprintf(" (try again in %d seconds)", n)
			}
		}
		return nil, fmt.Errorf("%w: AniList says too many requests%s", ErrOffline, wait)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAniList))
	if err != nil {
		return nil, fmt.Errorf("%w: AniList: %v", ErrOffline, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: AniList answered HTTP %d", ErrOffline, resp.StatusCode)
	}
	return parseAniList(raw)
}

func parseAniList(raw []byte) (map[int][]string, error) {
	var ans aniListAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("%w: AniList sent something that isn't its usual answer", ErrOffline)
	}
	if len(ans.Errors) > 0 && len(ans.Data.Page.Media) == 0 {
		return nil, fmt.Errorf("%w: AniList said %q", ErrOffline, ans.Errors[0].Message)
	}
	out := make(map[int][]string, len(ans.Data.Page.Media))
	for _, m := range ans.Data.Page.Media {
		names := append([]string{m.Title.Romaji, m.Title.English}, m.Synonyms...)
		out[m.ID] = cleanTitles(names)
	}
	return out, nil
}

// cleanTitles keeps the names that can show up in a release name: written in
// the Latin alphabet only (a Japanese, Arabic or Cyrillic name, or one with a
// few such words in it, never does), with a few letters or digits. Each is
// kept once, and no more than a dozen.
func cleanTitles(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		key := strings.ToLower(n)
		if n == "" || seen[key] || latinCount(n) < 3 || otherAlphabet(n) {
			continue
		}
		seen[key] = true
		out = append(out, n)
		if len(out) == maxTitles {
			break
		}
	}
	return out
}

// otherAlphabet reports whether s has a letter that is not Latin.
func otherAlphabet(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

func latinCount(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			n++
		}
	}
	return n
}
