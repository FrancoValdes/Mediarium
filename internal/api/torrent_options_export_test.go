package api

// TestStopTorrents stops every torrent the built-in client runs, so a test can
// end without a seeding torrent keeping its folder open.
func (s *Server) TestStopTorrents() int { return s.torrents.stopAll() }

// TestTorrentSkipped lists the files a running torrent leaves out.
func (s *Server) TestTorrentSkipped(id int64) []string {
	if ctl := s.torrents.control(id); ctl != nil {
		return ctl.Skipped()
	}
	return nil
}
