package api

// musicPlayerRoutes adds the routes of the music player's own features:
// playlists, lyrics, ReplayGain, the player settings and ListenBrainz
// scrobbling. All of them answer 404 while the music module is off (the route
// table's gate covers every /api/music/ path).
func (s *Server) musicPlayerRoutes(member, play, admin routeGroup) {
	// Playlists belong to the account; the handlers only show others' lists
	// that are shared and only let the owner change one.
	member.HandleFunc("GET /api/music/playlists", s.handleListPlaylists)
	member.HandleFunc("POST /api/music/playlists", s.handleCreatePlaylist)
	member.HandleFunc("GET /api/music/playlists/{id}", s.handleGetPlaylist)
	member.HandleFunc("PUT /api/music/playlists/{id}", s.handleUpdatePlaylist)
	member.HandleFunc("DELETE /api/music/playlists/{id}", s.handleDeletePlaylist)
	member.HandleFunc("POST /api/music/playlists/{id}/tracks", s.handleAddToPlaylist)
	member.HandleFunc("DELETE /api/music/playlists/{id}/tracks/{entry}", s.handleRemoveFromPlaylist)
	member.HandleFunc("PUT /api/music/playlists/{id}/order", s.handleReorderPlaylist)

	// What the player reads from a track's file.
	play.HandleFunc("GET /api/music/tracks/{id}/lyrics", s.handleTrackLyrics)
	play.HandleFunc("GET /api/music/tracks/{id}/gain", s.handleTrackGain)
	member.HandleFunc("GET /api/music/player-settings", s.handleGetPlayerSettings)
	admin.HandleFunc("PUT /api/music/player-settings", s.handlePutPlayerSettings)

	// The caller's own ListenBrainz connection.
	member.HandleFunc("GET /api/music/listenbrainz", s.handleScrobbleStatus)
	member.HandleFunc("PUT /api/music/listenbrainz", s.handleScrobbleSave)
	member.HandleFunc("DELETE /api/music/listenbrainz", s.handleScrobbleForget)
	play.HandleFunc("POST /api/music/listenbrainz/playing", s.handleScrobblePlaying)
	play.HandleFunc("POST /api/music/listenbrainz/listen", s.handleScrobbleListen)
}
