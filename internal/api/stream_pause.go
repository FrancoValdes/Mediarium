package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/mediaservers"
	"github.com/rdborg/mediarium/internal/queue"
	"github.com/rdborg/mediarium/internal/settings"
)

// Pause downloads while someone is watching. With the switch on, Mediarium
// asks the Plex, Jellyfin and Emby servers every half minute whether anyone
// is watching. While someone is, running and waiting downloads are paused;
// when nobody has been for a minute, the ones Mediarium paused are put back.
// It only ever resumes what it paused itself, and it keeps that list in the
// settings so a restart can't leave downloads paused for good.

const (
	streamPauseEvery = 30 * time.Second
	// streamQuietChecks is how many checks in a row must find nobody watching
	// before downloads carry on (two is about a minute), so a pause between
	// two episodes doesn't make them stop and start again.
	streamQuietChecks = 2
	// When no media server can be asked (down, or its address changed) while
	// downloads are held, they are given back after this many checks in a row
	// that also span at least streamUnreachableFor: nobody can say anyone is
	// still watching, and they must not stay paused for good.
	streamUnreachableChecks = 3
	streamUnreachableFor    = 10 * time.Minute
)

type streamPauseState struct {
	mu       sync.Mutex
	quiet    int
	watching int
	servers  int
	checked  time.Time
	// unreachable counts the checks in a row that could ask no media server,
	// since unreachableSince.
	unreachable      int
	unreachableSince time.Time
}

func (s *Server) streamPauseOn() bool {
	v, _ := s.Settings.Get(settings.KeyPauseWhileStreaming)
	return v == "1"
}

// pausedForStreaming is the ids of the downloads Mediarium paused because of a stream.
func (s *Server) pausedForStreaming() []int64 {
	raw, _ := s.Settings.Get(settings.KeyPausedForStreaming)
	var ids []int64
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &ids)
	}
	return ids
}

func (s *Server) savePausedForStreaming(ids []int64) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	val := ""
	if len(ids) > 0 {
		b, _ := json.Marshal(ids)
		val = string(b)
	}
	if err := s.Settings.Set(settings.KeyPausedForStreaming, val, false); err != nil {
		slog.Warn("pause while streaming: couldn't save the list of paused downloads", "err", err)
	}
}

// watchingNow counts the people watching across the media servers that can
// say so. ok is false when none of them could be asked; configured is how many
// enabled servers there are to ask (-1 when the list couldn't be read).
func (s *Server) watchingNow(ctx context.Context) (n, servers, configured int, ok bool) {
	list, err := s.MediaServers.List()
	if err != nil {
		return 0, 0, -1, false
	}
	for _, m := range list {
		if !m.Enabled || (m.Kind != mediaservers.KindPlex && m.Kind != mediaservers.KindJellyfin && m.Kind != mediaservers.KindEmby) {
			continue
		}
		configured++
		ask, cancel := context.WithTimeout(ctx, 10*time.Second)
		c, err := s.mediaClient.ActiveStreams(ask, m)
		cancel()
		if err != nil {
			slog.Debug("pause while streaming: couldn't ask a media server", "server", m.Name, "err", err)
			continue
		}
		servers++
		n += c
	}
	return n, servers, configured, servers > 0
}

// streamPauseRound makes one check and does what it says.
func (s *Server) streamPauseRound(ctx context.Context) {
	st := &s.streamPause
	if !s.streamPauseOn() {
		// Switched off: give back what was held.
		st.mu.Lock()
		st.watching, st.quiet = 0, 0
		st.mu.Unlock()
		if ids := s.pausedForStreaming(); len(ids) > 0 {
			s.resumeStreamPaused(ids)
		}
		return
	}
	n, servers, configured, ok := s.watchingNow(ctx)
	st.mu.Lock()
	st.checked, st.servers = time.Now(), servers
	if !ok {
		if st.unreachable == 0 {
			st.unreachableSince = time.Now()
		}
		st.unreachable++
		// Every media server was removed or switched off: nobody can be
		// watching through one, so what was held goes back at once. When they
		// are only out of reach, it goes back after a while.
		release := configured == 0 ||
			(st.unreachable >= streamUnreachableChecks && time.Since(st.unreachableSince) >= streamUnreachableFor)
		if release {
			st.watching, st.quiet = 0, 0
		}
		st.mu.Unlock()
		if release {
			if ids := s.pausedForStreaming(); len(ids) > 0 {
				slog.Info("pause while streaming: no media server can be asked, giving the downloads back", "held", len(ids))
				s.resumeStreamPaused(ids)
			}
		}
		return // otherwise change nothing
	}
	st.unreachable = 0
	st.watching = n
	if n > 0 {
		st.quiet = 0
	} else {
		st.quiet++
	}
	quiet := st.quiet
	st.mu.Unlock()

	switch {
	case n > 0:
		s.pauseForStreaming()
	case quiet >= streamQuietChecks:
		if ids := s.pausedForStreaming(); len(ids) > 0 {
			s.resumeStreamPaused(ids)
		}
	}
}

// pauseForStreaming pauses every download that is running or waiting.
// Importing is left to finish: it uses no bandwidth.
func (s *Server) pauseForStreaming() {
	items, err := s.QueueRepo.List()
	if err != nil {
		return
	}
	queueControlMu.Lock()
	defer queueControlMu.Unlock()
	defer s.dispatch.Hold()()
	held := s.pausedForStreaming()
	have := map[int64]bool{}
	for _, id := range held {
		have[id] = true
	}
	var newly []int64
	for _, it := range items {
		if it.Status != queue.StatusQueued && it.Status != queue.StatusDownloading {
			continue
		}
		if s.pauseItem(it) == nil && !have[it.ID] {
			have[it.ID] = true
			held = append(held, it.ID)
			newly = append(newly, it.ID)
		}
	}
	if len(newly) > 0 {
		s.savePausedForStreaming(held)
		slog.Info("downloads paused while someone is watching", "paused", len(newly))
	}
}

// resumeStreamPaused puts back the downloads Mediarium paused, the ones that
// are still paused (one a person stopped or removed meanwhile is left alone),
// oldest first, and forgets the list.
func (s *Server) resumeStreamPaused(ids []int64) {
	queueControlMu.Lock()
	defer queueControlMu.Unlock()
	defer s.dispatch.Hold()()
	// Each one goes to the front of the line, so the newest goes back first.
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	n := 0
	for _, id := range ids {
		it, err := s.QueueRepo.Get(id)
		if err != nil || it.Status != queue.StatusPaused {
			continue
		}
		if s.resumeItem(it) == nil {
			n++
		}
	}
	s.savePausedForStreaming(nil)
	if n > 0 {
		slog.Info("downloads carry on: nobody is watching", "resumed", n)
	}
}

// StartStreamPause runs the check in the background.
func (s *Server) StartStreamPause() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
		tick := time.NewTicker(streamPauseEvery)
		defer tick.Stop()
		for {
			s.streamPauseRound(ctx)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

type streamPauseAnswer struct {
	Enabled  bool `json:"enabled"`
	Watching int  `json:"watching"` // people watching at the last check
	Servers  int  `json:"servers"`  // media servers that could be asked
	Held     int  `json:"held"`     // downloads paused because of a stream, right now
}

func (s *Server) streamPauseAnswer() streamPauseAnswer {
	st := &s.streamPause
	st.mu.Lock()
	defer st.mu.Unlock()
	return streamPauseAnswer{Enabled: s.streamPauseOn(), Watching: st.watching, Servers: st.servers, Held: len(s.pausedForStreaming())}
}

// handleGetStreamPause shows the switch and what the last check found.
func (s *Server) handleGetStreamPause(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.streamPauseAnswer())
}

// handlePutStreamPause turns the switch on or off. Switching it off gives
// back the downloads it was holding.
func (s *Server) handlePutStreamPause(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "Send {\"enabled\": true} or {\"enabled\": false}.")
		return
	}
	val := "0"
	if *req.Enabled {
		val = "1"
	}
	if err := s.Settings.Set(settings.KeyPauseWhileStreaming, val, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !*req.Enabled {
		s.streamPauseRound(r.Context()) // gives back what was held
	}
	writeJSON(w, http.StatusOK, s.streamPauseAnswer())
}
