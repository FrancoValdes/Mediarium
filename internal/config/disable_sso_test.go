package config

import "testing"

func TestDisableSSOVariable(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"no", false},
		{"false", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{" yes ", true},
		{"on", true},
	}
	for _, tc := range tests {
		t.Run("value "+tc.value, func(t *testing.T) {
			t.Setenv("MEDIARIUM_DISABLE_SSO", tc.value)
			if got := Load().DisableSSO; got != tc.want {
				t.Fatalf("MEDIARIUM_DISABLE_SSO=%q gave %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
