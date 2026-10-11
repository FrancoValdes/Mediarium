package api

// TestSetKoboStore points the Kobo store passthrough at a fake server.
func (s *Server) TestSetKoboStore(baseURL string) {
	rt := s.ereaderState()
	rt.storeMu.Lock()
	rt.storeBase = baseURL
	rt.storeMu.Unlock()
}

// LimitKepubEntryForTest lowers the size one file of an EPUB may unpack to
// before it is not converted, and returns the function that puts it back.
func LimitKepubEntryForTest(bytes uint64) (restore func()) {
	old := kepubZipLimits
	kepubZipLimits.entry = bytes
	return func() { kepubZipLimits = old }
}
