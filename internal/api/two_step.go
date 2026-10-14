package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rdborg/mediarium/internal/auth"
	"github.com/rdborg/mediarium/internal/httpsec"
	"github.com/rdborg/mediarium/internal/qr"
	"github.com/rdborg/mediarium/internal/settings"
	"github.com/rdborg/mediarium/internal/totp"
)

// Two-step sign-in. With it on, the password alone doesn't sign anyone in:
// the sign-in waits (a "challenge", five minutes, five tries) for the
// six-digit code from the person's authenticator app, or one of their
// recovery codes. It is never forced; Mediarium suggests it once it can be
// reached from outside the home (a public address or sign-in through a proxy
// is set, or someone signed in from outside). API keys and sign-in through a
// proxy don't use it.

const (
	twoStepChallengeFor = 5 * time.Minute
	twoStepMaxTries     = 5
	outsideSignInFresh  = 30 * 24 * time.Hour
)

type twoStepChallenge struct {
	userID  int64
	expires time.Time
	tries   int
}

type twoStepChallenges struct {
	mu sync.Mutex
	m  map[string]*twoStepChallenge
}

// issue starts waiting for userID's code and answers the challenge token.
func (c *twoStepChallenges) issue(userID int64, now time.Time) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*twoStepChallenge{}
	}
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	c.m[token] = &twoStepChallenge{userID: userID, expires: now.Add(twoStepChallengeFor)}
	return token, nil
}

// try counts one attempt at a challenge and answers its account, or false
// when it has expired or been tried too often (it is then dropped).
func (c *twoStepChallenges) try(token string, now time.Time) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.m[token]
	if !ok {
		return 0, false
	}
	ch.tries++
	if now.After(ch.expires) || ch.tries > twoStepMaxTries {
		delete(c.m, token)
		return 0, false
	}
	return ch.userID, true
}

func (c *twoStepChallenges) drop(token string) {
	c.mu.Lock()
	delete(c.m, token)
	c.mu.Unlock()
}

// publicAddress reports whether ip is on the internet rather than the home
// network (loopback, private, link-local and carrier-grade NAT ranges, the
// last being where Tailscale lives, count as home).
func publicAddress(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	return !(a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() || cgnat.Contains(a))
}

// noteSignIn remembers when someone last signed in from outside the home.
func (s *Server) noteSignIn(r *http.Request) {
	if publicAddress(s.clientIP(r)) {
		_ = s.Settings.Set(settings.KeyAuthOutsideSignIn, time.Now().UTC().Format(time.RFC3339), false)
	}
}

// twoStepReason says why two-step sign-in is worth switching on, or "" when
// Mediarium looks like it is only reachable at home.
func (s *Server) twoStepReason(now time.Time) string {
	if v, _ := s.Settings.Get(settings.KeyPublicURL); strings.HasPrefix(strings.TrimSpace(v), "https://") {
		return "Mediarium has a public address, so it can be reached from outside your home."
	}
	if s.proxyHeader() != "" {
		return "People sign in through your reverse proxy, so Mediarium can be reached from outside your home."
	}
	if v, _ := s.Settings.Get(settings.KeyAuthOutsideSignIn); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil && now.Sub(t) < outsideSignInFresh {
			return "Someone signed in from outside your home recently."
		}
	}
	return ""
}

// finishSignIn signs a user in with a password once everything they need has
// been given.
func (s *Server) finishSignIn(w http.ResponseWriter, r *http.Request, user *auth.User, extra map[string]any) {
	s.finishSignInVia(w, r, user, auth.MethodPassword, extra)
}

// disabledMessage is what someone sees when their account was turned off.
const disabledMessage = "This account is turned off. Ask an administrator to turn it on again."

// finishSignInVia is finishSignIn that records how the person signed in. An
// account that was turned off never gets a session, whatever way it came in.
func (s *Server) finishSignInVia(w http.ResponseWriter, r *http.Request, user *auth.User, method string, extra map[string]any) {
	if user.Disabled {
		writeError(w, http.StatusForbidden, disabledMessage)
		return
	}
	if err := s.Auth.RecordLogin(user.ID); err != nil {
		slog.Warn("auth: record sign-in", "err", err) // bookkeeping only
	}
	s.noteSignIn(r)
	s.startSessionVia(w, r, user.ID, method)
	out := userPayload(user)
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// askForCode answers a correct password when two-step sign-in is on: no
// session yet, only a challenge for the code.
func (s *Server) askForCode(w http.ResponseWriter, user *auth.User) {
	token, err := s.twoStep.issue(user.ID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't sign you in. Try again.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"twoStep": true, "challenge": token})
}

const codeExpiredMessage = "That sign-in took too long or had too many tries. Enter your password again."

// POST /api/auth/login/code {"challenge", "code"} or {"challenge", "recoveryCode"}
func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	ipKey := httpsec.LimitKey(ip)
	if !s.LoginLimiter.Allow(ipKey) {
		writeError(w, http.StatusTooManyRequests, "Too many sign-in attempts. Wait a few minutes and try again.")
		return
	}
	var req struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recoveryCode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPublicBody)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Couldn't read that request. Reload the page and try again.")
		return
	}
	now := time.Now()
	userID, ok := s.twoStep.try(req.Challenge, now)
	if !ok {
		writeError(w, http.StatusUnauthorized, codeExpiredMessage)
		return
	}
	user, err := s.Auth.GetUser(userID)
	if err != nil || user.Disabled {
		writeError(w, http.StatusUnauthorized, codeExpiredMessage)
		return
	}
	if !s.localSignInOn() {
		writeError(w, http.StatusForbidden, localOffMessage)
		return
	}
	st, err := s.Auth.TwoStepOf(userID)
	if err != nil || !st.Enabled {
		s.twoStep.drop(req.Challenge)
		writeError(w, http.StatusUnauthorized, codeExpiredMessage)
		return
	}
	// Wrong codes are also counted per account, from every address and every challenge together, so
	// signing in again with the password does not give a fresh set of guesses.
	byUser := s.sec().byUser
	codeKey := "code:" + strconv.FormatInt(userID, 10)
	if !byUser.Allow(codeKey) {
		writeError(w, http.StatusTooManyRequests, "Too many wrong codes. Wait a few minutes and try again.")
		return
	}
	extra := map[string]any{}
	good := false
	if rc := strings.TrimSpace(req.RecoveryCode); rc != "" {
		good, err = s.Auth.UseRecoveryCode(userID, rc)
		if good {
			slog.Warn("auth: signed in with a recovery code", "user", user.Username, "ip", ip)
			if after, e := s.Auth.TwoStepOf(userID); e == nil {
				extra["recoveryLeft"] = after.RecoveryLeft
			}
		}
	} else if secret, e := s.box.Decrypt(st.Secret); e == nil {
		if ok, step := totp.Verify(secret, req.Code, now, st.LastStep); ok {
			good, err = s.Auth.UseStep(userID, step)
		}
	} else {
		err = e
	}
	if err != nil {
		slog.Error("auth: check a sign-in code", "err", err)
		writeError(w, http.StatusInternalServerError, "Couldn't sign you in. Try again.")
		return
	}
	if !good {
		s.LoginLimiter.RecordFailure(ipKey)
		byUser.RecordFailure(codeKey)
		slog.Warn("auth: wrong sign-in code", "user", user.Username, "ip", ip)
		writeError(w, http.StatusUnauthorized, "That code isn't right. Use the newest code in your app, and check your phone sets its time automatically.")
		return
	}
	s.twoStep.drop(req.Challenge)
	s.LoginLimiter.RecordSuccess(ipKey)
	byUser.RecordSuccess(codeKey)
	s.finishSignIn(w, r, user, extra)
}

// refuseAPIKeyFor answers 403 when an API key is used for something only
// the person themselves may do in a browser.
func refuseAPIKeyFor(w http.ResponseWriter, r *http.Request, what string) bool {
	if r.Header.Get("X-API-Key") == "" {
		return false
	}
	writeError(w, http.StatusForbidden, what+" can only be changed from your profile while signed in, not with an API key.")
	return true
}

type twoStepStatus struct {
	Enabled      bool   `json:"enabled"`
	RecoveryLeft int    `json:"recoveryLeft"`
	Suggested    string `json:"suggested,omitempty"` // why switching it on is a good idea, while it is off
}

// GET /api/auth/two-step: the signed-in person's own state.
func (s *Server) handleTwoStepStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	st, err := s.Auth.TwoStepOf(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := twoStepStatus{Enabled: st.Enabled, RecoveryLeft: st.RecoveryLeft}
	if !st.Enabled {
		out.Suggested = s.twoStepReason(time.Now())
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/auth/two-step/setup: a new secret to add to an authenticator
// app, as a QR code and as text. Nothing changes for signing in until the
// first code is confirmed (enable).
func (s *Server) handleTwoStepSetup(w http.ResponseWriter, r *http.Request) {
	if refuseAPIKeyFor(w, r, "Two-step sign-in") {
		return
	}
	user := auth.UserFromContext(r.Context())
	if !user.IsLocal() {
		writeError(w, http.StatusBadRequest, "Your account signs in through single sign-on or the directory, which has its own second step if you want one.")
		return
	}
	secret, err := totp.NewSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enc, err := s.box.Encrypt(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Auth.StartTwoStep(user.ID, enc); errors.Is(err, auth.ErrTwoStepOn) {
		writeError(w, http.StatusConflict, "Two-step sign-in is already on. Switch it off first to set it up again.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	uri := totp.URI("Mediarium", user.Username, secret)
	code, err := qr.Encode([]byte(uri))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The key in groups of four, as people type it in by hand.
	var grouped []string
	for i := 0; i < len(secret); i += 4 {
		grouped = append(grouped, secret[i:min(i+4, len(secret))])
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": strings.Join(grouped, " "), "uri": uri, "qr": code.Rows()})
}

// POST /api/auth/two-step/enable {"code"}: the first code from the app
// proves it was added; two-step sign-in is on from then, and the answer
// carries the recovery codes, shown once.
func (s *Server) handleTwoStepEnable(w http.ResponseWriter, r *http.Request) {
	if refuseAPIKeyFor(w, r, "Two-step sign-in") {
		return
	}
	user := auth.UserFromContext(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Enter the six-digit code your app shows.")
		return
	}
	st, err := s.Auth.TwoStepOf(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.Enabled {
		writeError(w, http.StatusConflict, "Two-step sign-in is already on.")
		return
	}
	if st.Secret == "" {
		writeError(w, http.StatusConflict, "Start the setup first.")
		return
	}
	secret, err := s.box.Decrypt(st.Secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok, step := totp.Verify(secret, req.Code, time.Now(), 0)
	if !ok {
		writeError(w, http.StatusBadRequest, "That code isn't right. Use the newest code in your app, and check your phone sets its time automatically.")
		return
	}
	codes, err := s.Auth.EnableTwoStep(user.ID, step)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("auth: two-step sign-in switched on", "user", user.Username)
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// checkPassword confirms the signed-in person's password before a change to
// their two-step sign-in, a new passkey or a single sign-on link. Wrong tries
// count per address and per account (together with wrong tries when changing
// the password), so a stolen session can't guess from many addresses.
func (s *Server) checkPassword(w http.ResponseWriter, r *http.Request, user *auth.User, password string) bool {
	ipKey := httpsec.LimitKey(s.clientIP(r))
	byUser := s.sec().byUser
	guessKey := passwordGuessKey(user.ID)
	if !s.LoginLimiter.Allow(ipKey) || !byUser.Allow(guessKey) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a few minutes and try again.")
		return false
	}
	if _, err := s.Auth.Authenticate(user.Username, password); err != nil {
		s.LoginLimiter.RecordFailure(ipKey)
		byUser.RecordFailure(guessKey)
		writeError(w, http.StatusBadRequest, "That password isn't right.")
		return false
	}
	return true
}

// passwordGuessKey is the per-account count of wrong tries at a signed-in
// person's own password.
func passwordGuessKey(userID int64) string {
	return "password:" + strconv.FormatInt(userID, 10)
}

// POST /api/auth/two-step/disable {"password"}
func (s *Server) handleTwoStepDisable(w http.ResponseWriter, r *http.Request) {
	if refuseAPIKeyFor(w, r, "Two-step sign-in") {
		return
	}
	user := auth.UserFromContext(r.Context())
	var req struct {
		Password string `json:"password"`
	}
	_ = decodeJSON(r, &req)
	if !s.checkPassword(w, r, user, req.Password) {
		return
	}
	if err := s.Auth.DisableTwoStep(user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Warn("auth: two-step sign-in switched off", "user", user.Username)
	writeJSON(w, http.StatusOK, nil)
}

// POST /api/auth/two-step/recovery {"password"}: a new set of recovery codes
// (the old ones stop working).
func (s *Server) handleTwoStepRecovery(w http.ResponseWriter, r *http.Request) {
	if refuseAPIKeyFor(w, r, "Two-step sign-in") {
		return
	}
	user := auth.UserFromContext(r.Context())
	var req struct {
		Password string `json:"password"`
	}
	_ = decodeJSON(r, &req)
	if !s.checkPassword(w, r, user, req.Password) {
		return
	}
	codes, err := s.Auth.NewRecoveryCodes(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// DELETE /api/users/{id}/two-step: an administrator switches it off for
// someone who lost their phone and their recovery codes.
func (s *Server) handleUserTwoStepOff(w http.ResponseWriter, r *http.Request) {
	if !requireSession(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "That isn't a valid account ID.")
		return
	}
	target, err := s.Auth.GetUser(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "That account doesn't exist.")
		return
	}
	if err := s.Auth.DisableTwoStep(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	by := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		by = u.Username
	}
	slog.Warn("auth: two-step sign-in switched off by an administrator", "user", target.Username, "by", by)
	writeJSON(w, http.StatusOK, nil)
}
