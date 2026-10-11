package announce

// Preset is a ready-made template for a common style of announcement. The
// server, channel and announcer names are not part of it: they differ per
// tracker and are on the tracker's own help pages.
//
// The patterns follow the formats that four trackers announce, as published
// in the MIT-licensed indexer definitions of autobrr
// (https://github.com/autobrr/autobrr, internal/indexer/definitions):
//
//   - one-line-details-link: the format of IPTorrents (iptorrents.yaml)
//   - one-line-quoted-name:  the format of TorrentLeech (torrentleech.yaml)
//   - one-line-new-torrent:  the format of HDBits (hdb.yaml)
//   - multi-line:            the format of BroadcasTheNet (btn.yaml)
//
// The example lines are made up (reserved .example addresses, invented
// releases) except for their shape.
type Preset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Template    Template `json:"template"`
	// Keys are the tracker keys the download address needs, by name, with a
	// hint for where to find each one.
	Keys []PresetKey `json:"keys"`
	// Auth is the sign-in method that usually fits this style.
	Auth string `json:"auth"`
	// InviteHint is shown in the invite command box ("" when the style rarely
	// uses one).
	InviteHint string `json:"inviteHint"`
	// Examples are announcement lines that this template reads, for the test box.
	Examples []string `json:"examples"`
}

// PresetKey names one key and says where to get it.
type PresetKey struct {
	Name string `json:"name"`
	Hint string `json:"hint"`
}

// Presets returns the built-in templates.
func Presets() []Preset {
	return []Preset{
		{
			ID:          "one-line-details-link",
			Name:        "One line: category, name, link and size",
			Description: "[Category] Release name - https://tracker.example/details.php?id=123 - 7.63 GB, with FREELEECH after the name when it applies.",
			Template: Template{
				Patterns:    []string{`\[(?P<category>.+?)] (?P<releaseName>.*?)\s*(?:(?P<freeleech>FREELEECH))? - (?P<baseUrl>https?\:\/\/.+\/).+id=(?P<torrentId>\d+) - (?P<torrentSize>.*)`},
				SizeUnits:   "binary",
				DownloadURL: "/download.php/{{ .torrentId }}/{{ .releaseName }}.torrent?torrent_pass={{ .passkey }}",
			},
			Keys:       []PresetKey{{Name: "passkey", Hint: "Your passkey or torrent pass, from your profile on the tracker."}},
			Auth:       AuthNickServ,
			InviteHint: "",
			Examples: []string{
				"[Movies/HD] The Movie 2010 1080p BluRay x264-GROUP FREELEECH - https://tracker.example/details.php?id=100200 - 7.63 GB",
				"[TV/Web-DL] The Show S02E05 720p WEB-DL DDP5.1 H.264-GROUP - https://tracker.example/details.php?id=100201 - 1.9 GB",
			},
		},
		{
			ID:          "one-line-quoted-name",
			Name:        "One line: category, quoted name and uploader",
			Description: "New Torrent Announcement: <Category> Name:'Release name' uploaded by 'user' - https://tracker.example/torrent/123, with freeleech after the uploader when it applies.",
			Template: Template{
				Patterns:    []string{`New Torrent Announcement:\s*<(?P<category>[^>]*)>\s*Name:'(?P<releaseName>.*)' uploaded by '(?P<uploader>[^']*)'\s*(?P<freeleech>freeleech)*\s*-\s*(?P<baseUrl>https?\:\/\/[^\/]+\/)torrent\/(?P<torrentId>\d+)`},
				SizeUnits:   "binary",
				DownloadURL: "/rss/download/{{ .torrentId }}/{{ .rsskey }}/{{ .releaseName }}.torrent",
			},
			Keys:     []PresetKey{{Name: "rsskey", Hint: "The RSS key on your profile page."}},
			Auth:     AuthNickServ,
			Examples: []string{"New Torrent Announcement: <Movies :: 1080p>  Name:'The Movie 2010 1080p BluRay x264-GROUP' uploaded by 'someone' freeleech -  https://tracker.example/torrent/555001"},
		},
		{
			ID:          "one-line-new-torrent",
			Name:        "One line: New Torrent, type, uploader and size",
			Description: "New Torrent: Release name - Type: Movie (H.264, Encode) - Uploaded by: user - Size: 4.54 GiB - https://tracker.example/details.php?id=123. Usually needs an invite command with a key from the tracker.",
			Template: Template{
				Patterns:    []string{`New Torrent: (?P<releaseName>.+?) - Type: (?P<category>.+?) (?:\((?P<releaseTags>.+?)\))?\s*(?P<origin>Internal)?!?\s*(?P<tags>Exclusive)?!?\s*- Uploaded by: (?P<uploader>.+?) - Size: (?P<torrentSize>.+?) - (?P<baseUrl>https:\/\/.+?\/).+id=(?P<torrentId>\d+)`},
				SizeUnits:   "binary",
				DownloadURL: "/download.php?id={{ .torrentId }}&passkey={{ .passkey }}",
			},
			Keys:       []PresetKey{{Name: "passkey", Hint: "Your passkey, from your profile on the tracker."}},
			Auth:       AuthNickServ,
			InviteHint: "BotName announce YOURKEY",
			Examples: []string{
				"New Torrent: The Movie 2010 1080p Blu-ray AVC DTS-HD MA 5.1-GROUP - Type: Movie (H.264, Blu-ray/HD DVD) - Uploaded by: someone - Size: 37.61 GiB - https://tracker.example/details.php?id=12345&hit=1",
				"New Torrent: The Movie 2011 720p BluRay DD-EX 5.1 x264-GROUP - Type: Movie (H.264, Encode) Internal! - Uploaded by: someone - Size: 4.54 GiB - https://tracker.example/details.php?id=12346&hit=1",
			},
		},
		{
			ID:          "multi-line",
			Name:        "Several lines for each release",
			Description: "The announcement is spread over four lines: NOW BROADCASTING! [ name ], then the series, then the category and uploader, then the links.",
			Template: Template{
				Multi: true,
				Patterns: []string{
					`^NOW BROADCASTING! \[ (?P<releaseName>.*) \]`,
					`^\[ Title: .* \] \[ Series: (?P<title>.*) \]`,
					`^(?:\[ (?P<year>\d+) \] )?\[ (?P<category>.*) \] \[ (?P<releaseTags>.*) \] \[ Uploader: (?P<uploader>.*?) \](?: \[ Pretime: (?P<preTime>.*) \])?`,
					`\[ (?P<baseUrl>https?:\/\/.*\/).+id=(?P<groupId>\d+) \/ https?:\/\/.*\/.+id=(?P<torrentId>\d+) \]`,
				},
				SizeUnits:   "binary",
				DownloadURL: "/torrents.php?action=download&id={{ .torrentId }}&authkey={{ .authkey }}&torrent_pass={{ .torrent_pass }}",
			},
			Keys: []PresetKey{
				{Name: "authkey", Hint: "The auth key from your profile or the tracker's help page."},
				{Name: "torrent_pass", Hint: "The torrent pass from your profile."},
			},
			Auth: AuthNickServ,
			Examples: []string{
				"NOW BROADCASTING! [ The Show S06E07 720p WEB-DL DD 5.1 H.264 - LP ]",
				"[ Title: S06E07 ] [ Series: The Show ]",
				"[ 2010 ] [ Episode ] [ MKV | x264 | WEB | P2P ] [ Uploader: someone ]",
				"[ https://tracker.example/torrents.php?id=7338 / https://tracker.example/torrents.php?action=download&id=9116 ]",
			},
		},
	}
}

// PresetByID finds a built-in template.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
