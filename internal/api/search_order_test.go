package api

import (
	"reflect"
	"testing"
)

func TestNewestFirst(t *testing.T) {
	type item struct {
		title string
		year  int
	}
	tests := []struct {
		name  string
		query string
		in    []item
		want  []string
	}{
		{"remake above the older film", "dune", []item{{"Dune", 1984}, {"Dune", 2021}, {"Dune", 2026}}, []string{"Dune 2026", "Dune 2021", "Dune 1984"}},
		{"titles that start with the search come first", "dune", []item{{"Children of Dune", 2003}, {"Dune: Part Two", 2024}, {"Dune", 1984}}, []string{"Dune: Part Two 2024", "Dune 1984", "Children of Dune 2003"}},
		{"no year goes last in its group", "the", []item{{"The Old", 0}, {"The New", 2025}, {"The Mid", 2010}}, []string{"The New 2025", "The Mid 2010", "The Old 0"}},
		{"equal years keep the search order", "x", []item{{"X B", 2020}, {"X A", 2020}}, []string{"X B 2020", "X A 2020"}},
		{"a leading the does not count", "matrix", []item{{"The Matrix", 1999}, {"Matrix Show", 2011}, {"Other", 2030}}, []string{"Matrix Show 2011", "The Matrix 1999", "Other 2030"}},
		{"punctuation and case do not matter", "spider man", []item{{"Other", 2030}, {"Spider-Man", 2002}, {"SPIDER-MAN: Homecoming", 2017}}, []string{"SPIDER-MAN: Homecoming 2017", "Spider-Man 2002", "Other 2030"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]item(nil), tc.in...)
			newestFirst(in, tc.query, func(i item) string { return i.title }, func(i item) int { return i.year })
			var got []string
			for _, i := range in {
				got = append(got, i.title+" "+itoa(i.year))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
