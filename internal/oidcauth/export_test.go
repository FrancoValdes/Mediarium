package oidcauth

import "time"

// SetClockForTest changes what the manager thinks the time is.
func SetClockForTest(m *Manager, now func() time.Time) { m.now = now }
