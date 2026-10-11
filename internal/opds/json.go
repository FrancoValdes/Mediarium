package opds

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The OPDS 2.0 shapes (https://specs.opds.io/opds-2.0).

type jsonLink struct {
	Rel       any    `json:"rel,omitempty"` // a string, or a list when there are several
	Href      string `json:"href"`
	Type      string `json:"type,omitempty"`
	Title     string `json:"title,omitempty"`
	Templated bool   `json:"templated,omitempty"`
	Length    int64  `json:"length,omitempty"`
}

type jsonImage struct {
	Href string `json:"href"`
	Type string `json:"type,omitempty"`
}

type jsonPublicationMeta struct {
	Type       string   `json:"@type"`
	Title      string   `json:"title"`
	Author     []string `json:"author,omitempty"`
	Identifier string   `json:"identifier"`
	Language   string   `json:"language,omitempty"`
	Modified   string   `json:"modified"`
	Published  string   `json:"published,omitempty"`
	Desc       string   `json:"description,omitempty"`
	Belongs    *struct {
		Series []jsonSeries `json:"series"`
	} `json:"belongsTo,omitempty"`
}

type jsonSeries struct {
	Name     string `json:"name"`
	Position string `json:"position,omitempty"`
}

type jsonPublication struct {
	Metadata jsonPublicationMeta `json:"metadata"`
	Links    []jsonLink          `json:"links"`
	Images   []jsonImage         `json:"images,omitempty"`
}

type jsonFeedMeta struct {
	Title        string `json:"title"`
	NumberOfItem int    `json:"numberOfItems,omitempty"`
	ItemsPerPage int    `json:"itemsPerPage,omitempty"`
	CurrentPage  int    `json:"currentPage,omitempty"`
	Modified     string `json:"modified,omitempty"`
}

type jsonFeed struct {
	Metadata     jsonFeedMeta      `json:"metadata"`
	Links        []jsonLink        `json:"links"`
	Navigation   []jsonLink        `json:"navigation,omitempty"`
	Publications []jsonPublication `json:"publications,omitempty"`
}

func toJSONLink(l Link) jsonLink {
	return jsonLink{Rel: relValue(l.Rel), Href: l.Href, Type: l.Type, Title: l.Title, Templated: l.Templated, Length: l.Length}
}

// relValue turns "self previous" style input into a list and leaves a single
// relation as the plain string the examples in the spec use.
func relValue(rel string) any {
	if rel == "" {
		return nil
	}
	parts := strings.Fields(rel)
	if len(parts) > 1 {
		return parts
	}
	return rel
}

// JSON writes the feed as an OPDS 2.0 document. The links of the feed and its
// entries must already point at the JSON versions of the feeds.
func (f Feed) JSON() ([]byte, error) {
	out := jsonFeed{
		Metadata: jsonFeedMeta{Title: f.Title, NumberOfItem: f.TotalResults, ItemsPerPage: f.ItemsPerPage, CurrentPage: f.Page, Modified: stamp(f.Updated)},
		Links:    []jsonLink{},
	}
	for _, l := range f.Links {
		out.Links = append(out.Links, toJSONLink(l))
	}
	for _, e := range f.Entries {
		if f.Kind == Navigation {
			for _, l := range e.Links {
				if l.Rel == RelSelf {
					continue
				}
				jl := toJSONLink(Link{Href: l.Href, Type: l.Type, Title: e.Title})
				out.Navigation = append(out.Navigation, jl)
				break
			}
			continue
		}
		ident := e.Identifier
		if ident == "" {
			ident = e.ID
		}
		p := jsonPublication{Metadata: jsonPublicationMeta{
			Type: "http://schema.org/Book", Title: e.Title, Author: e.Authors, Identifier: ident, Language: e.Language,
			Modified: stamp(e.Updated), Published: e.Issued, Desc: e.Summary,
		}, Links: []jsonLink{}}
		if e.Series != "" {
			p.Metadata.Belongs = &struct {
				Series []jsonSeries `json:"series"`
			}{Series: []jsonSeries{{Name: e.Series, Position: e.SeriesPos}}}
		}
		for _, l := range e.Links {
			switch l.Rel {
			case RelImage, RelThumbnail:
				p.Images = append(p.Images, jsonImage{Href: l.Href, Type: l.Type})
			default:
				jl := toJSONLink(l)
				if l.Rel == RelAcquisition {
					jl.Rel = RelOpenAccess
				}
				p.Links = append(p.Links, jl)
			}
		}
		out.Publications = append(out.Publications, p)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // "&" stays "&" in a title
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
