package indexers

import (
	"testing"
	"time"
)

func TestFeedWait(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want time.Duration
	}{
		{"an indexer's own api", "https://api.nzbgeek.info", normalFeedWait},
		{"a plain path", "https://indexer.example/api", normalFeedWait},
		{"prowlarr feed by id", "http://172.30.0.254:9696/14", slowFeedWait},
		{"prowlarr feed by name and id", "http://prowlarr.lan/14/", slowFeedWait},
		{"jackett torznab", "http://jackett.lan/api/v2.0/indexers/rede/results/torznab/", slowFeedWait},
		{"jackett port", "http://nas:9117/whatever", slowFeedWait},
		{"not a url", "::::", normalFeedWait},
		{"empty", "", normalFeedWait},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FeedWait(tt.addr); got != tt.want {
				t.Errorf("FeedWait(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
