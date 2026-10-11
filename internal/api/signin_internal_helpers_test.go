package api

import "github.com/rdborg/mediarium/internal/auth"

func newTestUser(viewer, readOnlyKey, admin bool) *auth.User {
	return &auth.User{ID: 1, Username: "t", Viewer: viewer, ReadOnlyKey: readOnlyKey, IsAdmin: admin}
}
