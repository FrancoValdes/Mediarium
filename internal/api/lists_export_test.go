package api

import (
	"context"

	"github.com/rdborg/mediarium/internal/notify"
)

// Test hooks for the import lists and the request portal (package api_test
// calls these; the rest of the hooks are in export_test.go).

// TestListsNoPause removes the pauses a run keeps between requests, and
// TestSetMDBListBase points MDBList reads at a fixture server.
func (s *Server) TestListsNoPause()                      { s.lists.noPause = true }
func (s *Server) TestSetMDBListBase(base string)         { s.lists.mdblistBase = base }
func (s *Server) TestImportListsJob(ctx context.Context) { s.importListsJob(ctx) }

// TestRequestAvailableJob runs the check that tells people their request is
// ready to watch.
func (s *Server) TestRequestAvailableJob(ctx context.Context) { s.requestAvailableJob(ctx) }

type personRecorder struct {
	user int64
	got  func(userID int64, ev notify.Event)
}

func (p personRecorder) Send(_ context.Context, ev notify.Event) error {
	p.got(p.user, ev)
	return nil
}

// TestOnPersonMessage calls got for every message that would go to a person.
func (s *Server) TestOnPersonMessage(got func(userID int64, ev notify.Event)) {
	s.portal.extraSenders = func(userID int64) []notify.Sender {
		return []notify.Sender{personRecorder{user: userID, got: got}}
	}
}
