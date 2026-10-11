package quality

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rdborg/mediarium/internal/parser"
)

// What a quality profile does with custom formats: each format it gives a
// score adds that score to a release that matches it. The total (the format
// score) ranks releases of the same quality, a release below MinFormatScore
// is refused, and a file below CutoffFormatScore is replaced by a release of
// the same or better quality that scores higher.

// ScoredFormat is a format with the score a profile gives it.
type ScoredFormat struct {
	Format *CustomFormat
	Score  int
}

// FormatScore is a release title's format score and the formats it matched.
func (p Profile) FormatScore(title string) (int, []string) {
	if len(p.Formats) == 0 {
		return 0, nil
	}
	in := InputFor(title)
	in.OriginalLanguage = p.OriginalLanguage
	total := 0
	var names []string
	for _, sf := range p.Formats {
		if sf.Format != nil && sf.Format.Matches(in) {
			total += sf.Score
			names = append(names, sf.Format.Name)
		}
	}
	return total, names
}

// FormatsNeedOriginalLanguage reports whether any of the profile's formats
// has a condition on the title's original language.
func (p Profile) FormatsNeedOriginalLanguage() bool {
	for _, sf := range p.Formats {
		if sf.Format == nil {
			continue
		}
		for _, s := range sf.Format.Specs {
			if s.Kind == SpecLanguage && specNumber(s) == languageOriginal {
				return true
			}
		}
	}
	return false
}

// usesFormats reports whether format scores take part in this profile's
// decisions at all.
func (p Profile) usesFormats() bool {
	return len(p.Formats) > 0 || p.MinFormatScore != 0
}

// isSeasonPack reports whether a release is a whole season (or more).
func isSeasonPack(r parser.Release) bool {
	return (r.Season > 0 || r.Special) && len(r.Episodes) == 0
}

// formatRestrictions are the format and season-pack checks TitleAllowed adds.
func (p Profile) formatRestrictions(title string) (bool, string) {
	if p.NoSeasonPacks && isSeasonPack(parser.Parse(title)) {
		return false, fmt.Sprintf("is a season pack, which the %q profile never downloads", p.Name)
	}
	if p.usesFormats() {
		if score, _ := p.FormatScore(title); score < p.MinFormatScore {
			return false, fmt.Sprintf("scores %d with custom formats, and the %q profile needs at least %d", score, p.Name, p.MinFormatScore)
		}
	}
	return true, ""
}

// WantsFormatUpgrade reports whether a downloaded file (from the release
// named currentRelease) is below the profile's custom-format cutoff, so a
// better-scoring release should replace it. A file whose release isn't known
// is never replaced for its score: there would be nothing to compare.
func (p Profile) WantsFormatUpgrade(currentRelease string) bool {
	if !p.UpgradeAllowed || p.CutoffFormatScore <= 0 || strings.TrimSpace(currentRelease) == "" || len(p.Formats) == 0 {
		return false
	}
	score, _ := p.FormatScore(currentRelease)
	return score < p.CutoffFormatScore
}

// IsUpgradeOver is IsUpgradeOverTier with custom formats: besides a better
// quality, a release of the same quality is an upgrade when it scores higher
// than the current file's release and that file is still below the
// profile's format cutoff.
func (p Profile) IsUpgradeOver(currentTier Tier, currentRelease, candidateTitle string) bool {
	candidate := parser.Parse(candidateTitle)
	if p.IsUpgradeOverTier(currentTier, candidate) {
		return true
	}
	if !p.Accepts(candidate) || !p.WantsFormatUpgrade(currentRelease) {
		return false
	}
	if Rank(Classify(candidate)) < Rank(currentTier) {
		return false
	}
	current, _ := p.FormatScore(currentRelease)
	next, _ := p.FormatScore(candidateTitle)
	return next > current
}

// --- storage ---

func encodeFormatScores(m map[int64]int) string {
	out := map[string]int{}
	for id, s := range m {
		if s != 0 && id > 0 {
			out[strconv.FormatInt(id, 10)] = s
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func decodeFormatScores(s string) map[int64]int {
	raw := map[string]int{}
	_ = json.Unmarshal([]byte(s), &raw)
	out := map[int64]int{}
	for k, v := range raw {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil && id > 0 && v != 0 {
			out[id] = v
		}
	}
	return out
}

// maxFormatScore bounds one format's score and the profile's thresholds;
// TRaSH uses up to ±10,000 per format and sums can go well beyond.
const maxFormatScore = 1_000_000

func validateFormatScores(p *Profile) error {
	for id, s := range p.FormatScores {
		if s < -maxFormatScore || s > maxFormatScore {
			return fmt.Errorf("%w: the score for custom format %d is out of range", ErrInvalid, id)
		}
	}
	if p.MinFormatScore < -maxFormatScore || p.MinFormatScore > maxFormatScore || p.CutoffFormatScore < 0 || p.CutoffFormatScore > maxFormatScore {
		return fmt.Errorf("%w: the minimum and upgrade-until scores are out of range", ErrInvalid)
	}
	return nil
}

// FormatRepo stores custom formats.
type FormatRepo struct{ db *sql.DB }

func NewFormatRepo(db *sql.DB) *FormatRepo { return &FormatRepo{db: db} }

// ErrFormatName means another format already has that name.
var ErrFormatName = errors.New("a custom format with that name already exists")

func (r *FormatRepo) scan(scan func(dest ...any) error) (CustomFormat, error) {
	var f CustomFormat
	var app, specs string
	if err := scan(&f.ID, &f.Name, &app, &f.TrashID, &specs); err != nil {
		return CustomFormat{}, err
	}
	f.App = FormatApp(app)
	if err := json.Unmarshal([]byte(specs), &f.Specs); err != nil {
		return CustomFormat{}, fmt.Errorf("custom format %q: %w", f.Name, err)
	}
	f.Compile()
	return f, nil
}

const selectFormat = `SELECT id, name, app, trash_id, specs FROM custom_formats`

// List returns every custom format, by name.
func (r *FormatRepo) List() ([]CustomFormat, error) {
	rows, err := r.db.Query(selectFormat + ` ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list custom formats: %w", err)
	}
	defer rows.Close()
	var out []CustomFormat
	for rows.Next() {
		f, err := r.scan(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *FormatRepo) Get(id int64) (CustomFormat, error) {
	return r.scan(r.db.QueryRow(selectFormat+` WHERE id = ?`, id).Scan)
}

// Save stores a new format, or replaces the one with the same name (or, for
// a TRaSH format, the same TRaSH id) so importing again updates it. It
// returns the stored format and whether it was new.
func (r *FormatRepo) Save(f CustomFormat) (CustomFormat, bool, error) {
	specs, err := json.Marshal(f.Specs)
	if err != nil {
		return CustomFormat{}, false, err
	}
	var id int64
	err = r.db.QueryRow(`SELECT id FROM custom_formats WHERE (trash_id != '' AND trash_id = ?) OR name = ? COLLATE NOCASE ORDER BY trash_id = ? DESC LIMIT 1`, f.TrashID, f.Name, f.TrashID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := r.db.Exec(`INSERT INTO custom_formats (name, app, trash_id, specs) VALUES (?, ?, ?, ?)`, f.Name, string(f.App), f.TrashID, string(specs))
		if err != nil {
			return CustomFormat{}, false, fmt.Errorf("save custom format %q: %w", f.Name, err)
		}
		f.ID, _ = res.LastInsertId()
		return f, true, nil
	case err != nil:
		return CustomFormat{}, false, err
	}
	if _, err := r.db.Exec(`UPDATE custom_formats SET name = ?, app = ?, trash_id = ?, specs = ? WHERE id = ?`, f.Name, string(f.App), f.TrashID, string(specs), id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return CustomFormat{}, false, ErrFormatName
		}
		return CustomFormat{}, false, fmt.Errorf("update custom format %q: %w", f.Name, err)
	}
	f.ID = id
	return f, false, nil
}

// Rename renames a format.
func (r *FormatRepo) Rename(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: a name of 1-100 characters", ErrInvalid)
	}
	res, err := r.db.Exec(`UPDATE custom_formats SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrFormatName
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Delete removes a format and its score from every profile.
func (r *FormatRepo) Delete(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM custom_formats WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete custom format: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	rows, err := tx.Query(`SELECT id, format_scores FROM quality_profiles`)
	if err != nil {
		return err
	}
	type upd struct {
		id     int64
		scores string
	}
	var ups []upd
	for rows.Next() {
		var pid int64
		var s string
		if err := rows.Scan(&pid, &s); err != nil {
			rows.Close()
			return err
		}
		m := decodeFormatScores(s)
		if _, ok := m[id]; ok {
			delete(m, id)
			ups = append(ups, upd{pid, encodeFormatScores(m)})
		}
	}
	rows.Close()
	for _, u := range ups {
		if _, err := tx.Exec(`UPDATE quality_profiles SET format_scores = ? WHERE id = ?`, u.scores, u.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AttachFormats fills a profile's Formats from its FormatScores, best score
// first, leaving out formats that no longer exist.
func AttachFormats(p Profile, byID map[int64]*CustomFormat) Profile {
	p.Formats = nil
	for id, score := range p.FormatScores {
		if f, ok := byID[id]; ok && score != 0 {
			p.Formats = append(p.Formats, ScoredFormat{Format: f, Score: score})
		}
	}
	sort.Slice(p.Formats, func(i, j int) bool {
		if p.Formats[i].Score != p.Formats[j].Score {
			return p.Formats[i].Score > p.Formats[j].Score
		}
		return p.Formats[i].Format.Name < p.Formats[j].Format.Name
	})
	return p
}
