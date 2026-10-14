package api

import (
	"testing"
	"time"
)

func TestSpeedScheduleRules(t *testing.T) {
	// 2026-10-12 is a Monday.
	at := func(day, hour int) time.Time {
		return time.Date(2026, 10, 11+day, hour, 30, 0, 0, time.UTC) // day 0 = Sunday 11th
	}
	workdays := []int{1, 2, 3, 4, 5}
	rules := []speedRule{
		{Days: workdays, From: 8, To: 18, LimitMB: 2}, // working hours: slow
		{Days: []int{5}, From: 22, To: 6, LimitMB: 0}, // Friday night through early Saturday: free
		{Days: nil, From: 0, To: 0, LimitMB: 10},      // everything else: 10
	}
	cases := []struct {
		name      string
		day, hour int
		want      int
	}{
		{"Monday 9:00 is slow", 1, 9, 2},
		{"Monday 18:00 is past the rule", 1, 18, 10},
		{"Friday 23:00 is free", 5, 23, 0},
		{"early Saturday still belongs to Friday night", 6, 3, 0},
		{"Saturday 7:00 is past it", 6, 7, 10},
		{"Sunday any time", 0, 12, 10},
	}
	for _, c := range cases {
		got, ok := scheduledLimit(rules, at(c.day, c.hour))
		if !ok || got != c.want {
			t.Errorf("%s: got %d (matched %v), want %d", c.name, got, ok, c.want)
		}
	}
	if _, ok := scheduledLimit(rules[:1], at(0, 12)); ok {
		t.Error("with no line that fits, the plain limit applies")
	}
	if (speedRule{From: 24}).valid() || (speedRule{Days: []int{7}}).valid() || (speedRule{LimitMB: -1}).valid() {
		t.Error("out-of-range rules are invalid")
	}
}
