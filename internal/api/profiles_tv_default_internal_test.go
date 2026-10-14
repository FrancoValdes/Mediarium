package api

import (
	"testing"

	"github.com/rdborg/mediarium/internal/quality"
)

func TestResolveDefaultsPerKind(t *testing.T) {
	presets := quality.Presets()
	movieDef := presets[quality.Preset1080p]
	movieDef.ID = 1
	tvDef := presets[quality.DefaultPreset]
	tvDef.ID = 2
	own := presets[quality.Preset1080p]
	own.ID = 3
	own.Name = "Own"

	withTV := profileSet{byID: map[int64]quality.Profile{1: movieDef, 2: tvDef, 3: own}, def: movieDef, defTV: &tvDef}
	noTV := profileSet{byID: map[int64]quality.Profile{1: movieDef, 2: tvDef, 3: own}, def: movieDef}

	cases := []struct {
		name   string
		ps     profileSet
		tv     bool
		id     int64
		wantID int64
	}{
		{"movie with no profile uses the movie default", withTV, false, 0, 1},
		{"movie ignores the TV default", withTV, false, 0, 1},
		{"movie keeps its own profile", withTV, false, 3, 3},
		{"show with no profile uses the TV default", withTV, true, 0, 2},
		{"show with a deleted profile uses the TV default", withTV, true, 99, 2},
		{"show keeps its own profile", withTV, true, 3, 3},
		{"show with no TV default uses the movie default", noTV, true, 0, 1},
		{"movie with no TV default uses the movie default", noTV, false, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got quality.Profile
			if c.tv {
				got = c.ps.resolveTV(c.id)
			} else {
				got = c.ps.resolve(c.id)
			}
			if got.ID != c.wantID {
				t.Fatalf("got profile %d, want %d", got.ID, c.wantID)
			}
		})
	}
}
