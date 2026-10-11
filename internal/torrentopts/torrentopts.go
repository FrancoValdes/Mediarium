// Package torrentopts keeps what a person chose for the built-in torrent
// client beyond the global settings: options of one torrent (order, speed
// limits, files left out, category), the seeding rules of an indexer, and the
// list of categories with their folders.
package torrentopts

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Options are the choices made for one queue item's torrent. The zero value is
// "nothing chosen" except Sequential, where Follow is the default.
type Options struct {
	QueueID int64
	// Sequential is Follow (the global default), Off or On.
	Sequential   int
	DownKB       int      // KB/s limit of this torrent, 0 = none of its own
	UpKB         int      // the same for uploading
	FilesDecided bool     // the file selection was made
	SkipFiles    []string // paths inside the torrent that are left out
	Category     string
	Subfolder    string
	DirDecided   bool // the folder was chosen: a paused download keeps it
}

// Sequential values.
const (
	Follow = -1
	Off    = 0
	On     = 1
)

// Repo stores the options.
type Repo struct{ db *sql.DB }

// NewRepo returns a Repo on db.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

const optionColumns = `queue_id, sequential, down_limit_kb, up_limit_kb, files_decided, skip_files, category, subfolder, dir_decided`

func scanOptions(row interface{ Scan(...any) error }) (Options, error) {
	var o Options
	var decided, dirDecided int
	var skip string
	if err := row.Scan(&o.QueueID, &o.Sequential, &o.DownKB, &o.UpKB, &decided, &skip, &o.Category, &o.Subfolder, &dirDecided); err != nil {
		return Options{}, err
	}
	o.FilesDecided = decided != 0
	o.DirDecided = dirDecided != 0
	if err := json.Unmarshal([]byte(skip), &o.SkipFiles); err != nil {
		o.SkipFiles = nil
	}
	return o, nil
}

// Get returns the options of a queue item. A torrent nobody set anything for
// gets the defaults, with ok false.
func (r *Repo) Get(queueID int64) (o Options, ok bool, err error) {
	o, err = scanOptions(r.db.QueryRow(`SELECT `+optionColumns+` FROM torrent_options WHERE queue_id = ?`, queueID))
	if errors.Is(err, sql.ErrNoRows) {
		return Options{QueueID: queueID, Sequential: Follow}, false, nil
	}
	if err != nil {
		return Options{}, false, fmt.Errorf("read torrent options: %w", err)
	}
	return o, true, nil
}

// Categories lists the category of every queue item that has one, by queue id.
func (r *Repo) Categories() (map[int64]string, error) {
	rows, err := r.db.Query(`SELECT queue_id, category FROM torrent_options WHERE category <> ''`)
	if err != nil {
		return nil, fmt.Errorf("list torrent categories: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("list torrent categories: %w", err)
		}
		out[id] = name
	}
	return out, rows.Err()
}

// Subfolders lists the category folders in use by any download.
func (r *Repo) Subfolders() ([]string, error) {
	rows, err := r.db.Query(`SELECT DISTINCT subfolder FROM torrent_options WHERE subfolder <> ''`)
	if err != nil {
		return nil, fmt.Errorf("list torrent folders: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("list torrent folders: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetSequential stores the order choice: Follow, Off or On.
func (r *Repo) SetSequential(queueID int64, v int) error {
	if v != Follow && v != Off && v != On {
		return fmt.Errorf("sequential %d is not -1, 0 or 1", v)
	}
	return r.upsert(`sequential`, queueID, v)
}

// SetLimits stores the torrent's own speed limits in KB/s (0 = none).
func (r *Repo) SetLimits(queueID int64, downKB, upKB int) error {
	if downKB < 0 || upKB < 0 {
		return errors.New("a speed limit cannot be negative")
	}
	_, err := r.db.Exec(`INSERT INTO torrent_options (queue_id, down_limit_kb, up_limit_kb) VALUES (?, ?, ?)
		ON CONFLICT(queue_id) DO UPDATE SET down_limit_kb = excluded.down_limit_kb, up_limit_kb = excluded.up_limit_kb`, queueID, downKB, upKB)
	if err != nil {
		return fmt.Errorf("save torrent limits: %w", err)
	}
	return nil
}

// SetSkipped stores which files are left out, and marks the selection as made.
func (r *Repo) SetSkipped(queueID int64, files []string) error {
	if files == nil {
		files = []string{}
	}
	b, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("save skipped files: %w", err)
	}
	_, err = r.db.Exec(`INSERT INTO torrent_options (queue_id, files_decided, skip_files) VALUES (?, 1, ?)
		ON CONFLICT(queue_id) DO UPDATE SET files_decided = 1, skip_files = excluded.skip_files`, queueID, string(b))
	if err != nil {
		return fmt.Errorf("save skipped files: %w", err)
	}
	return nil
}

// SetCategory stores the category a download was filed under and its folder.
func (r *Repo) SetCategory(queueID int64, name, subfolder string) error {
	_, err := r.db.Exec(`INSERT INTO torrent_options (queue_id, category, subfolder, dir_decided) VALUES (?, ?, ?, 1)
		ON CONFLICT(queue_id) DO UPDATE SET category = excluded.category, subfolder = excluded.subfolder, dir_decided = 1`, queueID, name, subfolder)
	if err != nil {
		return fmt.Errorf("save torrent category: %w", err)
	}
	return nil
}

func (r *Repo) upsert(column string, queueID int64, v any) error {
	// column is one of this package's own names, never input.
	_, err := r.db.Exec(`INSERT INTO torrent_options (queue_id, `+column+`) VALUES (?, ?)
		ON CONFLICT(queue_id) DO UPDATE SET `+column+` = excluded.`+column, queueID, v)
	if err != nil {
		return fmt.Errorf("save torrent option: %w", err)
	}
	return nil
}

// Delete forgets the options of a queue item.
func (r *Repo) Delete(queueID int64) error {
	if _, err := r.db.Exec(`DELETE FROM torrent_options WHERE queue_id = ?`, queueID); err != nil {
		return fmt.Errorf("delete torrent options: %w", err)
	}
	return nil
}

// SeedRule overrides the global seeding limit for the torrents of one indexer.
// A nil field follows the global limit; 0 means no limit of that kind.
type SeedRule struct {
	Ratio *float64 `json:"ratio"`
	Hours *float64 `json:"hours"`
}

// Empty reports whether the rule changes nothing.
func (s SeedRule) Empty() bool { return s.Ratio == nil && s.Hours == nil }

// Limits for a rule: a ratio over 100 or more than a year of hours is a typo.
const (
	MaxSeedRatio = 100.0
	MaxSeedHours = 24 * 366.0
)

// Validate checks the numbers of a rule.
func (s SeedRule) Validate() error {
	if s.Ratio != nil && (*s.Ratio < 0 || *s.Ratio > MaxSeedRatio) {
		return fmt.Errorf("the seed ratio is 0 (no limit) to %g", MaxSeedRatio)
	}
	if s.Hours != nil && (*s.Hours < 0 || *s.Hours > MaxSeedHours) {
		return fmt.Errorf("the seed time is 0 (no limit) to %g hours", MaxSeedHours)
	}
	return nil
}

// SeedRule returns the rule of an indexer (empty when it has none).
func (r *Repo) SeedRule(indexerID int64) (SeedRule, error) {
	var ratio, hours sql.NullFloat64
	err := r.db.QueryRow(`SELECT ratio, hours FROM indexer_seed_rules WHERE indexer_id = ?`, indexerID).Scan(&ratio, &hours)
	if errors.Is(err, sql.ErrNoRows) {
		return SeedRule{}, nil
	}
	if err != nil {
		return SeedRule{}, fmt.Errorf("read seed rule: %w", err)
	}
	return ruleOf(ratio, hours), nil
}

func ruleOf(ratio, hours sql.NullFloat64) SeedRule {
	var s SeedRule
	if ratio.Valid {
		v := ratio.Float64
		s.Ratio = &v
	}
	if hours.Valid {
		v := hours.Float64
		s.Hours = &v
	}
	return s
}

// SeedRules lists every indexer's rule, by indexer id.
func (r *Repo) SeedRules() (map[int64]SeedRule, error) {
	rows, err := r.db.Query(`SELECT indexer_id, ratio, hours FROM indexer_seed_rules`)
	if err != nil {
		return nil, fmt.Errorf("list seed rules: %w", err)
	}
	defer rows.Close()
	out := map[int64]SeedRule{}
	for rows.Next() {
		var id int64
		var ratio, hours sql.NullFloat64
		if err := rows.Scan(&id, &ratio, &hours); err != nil {
			return nil, fmt.Errorf("list seed rules: %w", err)
		}
		out[id] = ruleOf(ratio, hours)
	}
	return out, rows.Err()
}

// SetSeedRule stores an indexer's rule; an empty rule removes it. The indexer
// has to exist.
func (r *Repo) SetSeedRule(indexerID int64, rule SeedRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.Empty() {
		if _, err := r.db.Exec(`DELETE FROM indexer_seed_rules WHERE indexer_id = ?`, indexerID); err != nil {
			return fmt.Errorf("remove seed rule: %w", err)
		}
		return nil
	}
	var ratio, hours sql.NullFloat64
	if rule.Ratio != nil {
		ratio = sql.NullFloat64{Float64: *rule.Ratio, Valid: true}
	}
	if rule.Hours != nil {
		hours = sql.NullFloat64{Float64: *rule.Hours, Valid: true}
	}
	_, err := r.db.Exec(`INSERT INTO indexer_seed_rules (indexer_id, ratio, hours) VALUES (?, ?, ?)
		ON CONFLICT(indexer_id) DO UPDATE SET ratio = excluded.ratio, hours = excluded.hours`, indexerID, ratio, hours)
	if err != nil {
		return fmt.Errorf("save seed rule: %w", err)
	}
	return nil
}

// Goal combines the global seeding limits with an indexer's rule: a field the
// rule leaves empty keeps the global value.
func (s SeedRule) Goal(globalRatio, globalHours float64) (ratio, hours float64) {
	ratio, hours = globalRatio, globalHours
	if s.Ratio != nil {
		ratio = *s.Ratio
	}
	if s.Hours != nil {
		hours = *s.Hours
	}
	return ratio, hours
}

// Category is a name for a group of torrents and the folder they are saved in,
// inside the downloads working folder. An empty Subfolder means the working
// folder itself, as before.
type Category struct {
	Name      string `json:"name"`
	Subfolder string `json:"subfolder"`
}

// Kinds of grab a category can be assigned to.
const (
	KindMovie = "movie"
	KindTV    = "tv"
	KindMusic = "music"
	KindBook  = "book"
)

// Kinds lists them in the order the settings page shows them.
var Kinds = []string{KindMovie, KindTV, KindMusic, KindBook}

// MaxCategories is how many categories there can be: it is a short list.
const MaxCategories = 12

// CategoryConfig is the saved category list and which category each kind of
// grab gets.
type CategoryConfig struct {
	Categories []Category        `json:"categories"`
	ByKind     map[string]string `json:"byKind"`
}

// ParseCategories reads the saved settings values. Bad or empty text gives an
// empty configuration: categories are off.
func ParseCategories(list, byKind string) CategoryConfig {
	var c CategoryConfig
	if strings.TrimSpace(list) != "" {
		_ = json.Unmarshal([]byte(list), &c.Categories)
	}
	if strings.TrimSpace(byKind) != "" {
		_ = json.Unmarshal([]byte(byKind), &c.ByKind)
	}
	if c.ByKind == nil {
		c.ByKind = map[string]string{}
	}
	return c
}

// For is the category a kind of grab is filed under. ok is false when it has
// none (the plain default: the working folder itself, no category).
func (c CategoryConfig) For(kind string) (Category, bool) {
	name := c.ByKind[kind]
	if name == "" {
		return Category{}, false
	}
	for _, cat := range c.Categories {
		if strings.EqualFold(cat.Name, name) {
			return cat, true
		}
	}
	return Category{}, false
}

// CleanSubfolder checks a folder name for a category: one plain folder name,
// no path, nothing that leads out of the working folder or looks like a
// download's own folder.
func CleanSubfolder(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > 60 {
		return "", errors.New("a folder name is at most 60 characters")
	}
	if s == "." || s == ".." || strings.HasPrefix(s, ".") {
		return "", errors.New("a folder name cannot start with a dot")
	}
	if strings.ContainsAny(s, `/\:*?"<>|`) {
		return "", errors.New(`a folder name cannot contain / \ : * ? " < > |`)
	}
	for _, r := range s {
		if r < 0x20 {
			return "", errors.New("a folder name cannot contain control characters")
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "queue-") {
		return "", errors.New(`a folder name cannot start with "queue-"`)
	}
	return s, nil
}

// Validate checks and tidies a configuration for saving: names are given and
// unique, folders are plain names (and unique), and every kind points at a
// category that exists.
func (c CategoryConfig) Validate() (CategoryConfig, error) {
	if len(c.Categories) > MaxCategories {
		return c, fmt.Errorf("there can be at most %d categories", MaxCategories)
	}
	out := CategoryConfig{ByKind: map[string]string{}}
	names := map[string]string{} // lower-case name -> name
	folders := map[string]bool{}
	for _, cat := range c.Categories {
		cat.Name = strings.TrimSpace(cat.Name)
		if cat.Name == "" {
			return c, errors.New("every category needs a name")
		}
		if len(cat.Name) > 40 {
			return c, errors.New("a category name is at most 40 characters")
		}
		if _, dup := names[strings.ToLower(cat.Name)]; dup {
			return c, fmt.Errorf("there are two categories called %q", cat.Name)
		}
		sub, err := CleanSubfolder(cat.Subfolder)
		if err != nil {
			return c, fmt.Errorf("category %q: %w", cat.Name, err)
		}
		if sub != "" {
			if folders[strings.ToLower(sub)] {
				return c, fmt.Errorf("two categories use the folder %q", sub)
			}
			folders[strings.ToLower(sub)] = true
		}
		cat.Subfolder = sub
		names[strings.ToLower(cat.Name)] = cat.Name
		out.Categories = append(out.Categories, cat)
	}
	for _, kind := range Kinds {
		name := strings.TrimSpace(c.ByKind[kind])
		if name == "" {
			continue
		}
		real, ok := names[strings.ToLower(name)]
		if !ok {
			return c, fmt.Errorf("%q is not in the category list", name)
		}
		out.ByKind[kind] = real
	}
	for kind := range c.ByKind {
		known := false
		for _, k := range Kinds {
			known = known || k == kind
		}
		if !known {
			return c, fmt.Errorf("%q is not a kind of download", kind)
		}
	}
	return out, nil
}
