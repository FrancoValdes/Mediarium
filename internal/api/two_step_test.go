package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/totp"
)

func TestTwoStepSignIn(t *testing.T) {
	_, base, admin := loginNewServer(t)
	login := func(user, pass string) (*http.Client, map[string]any) {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		res := postJSON[map[string]any](t, c, base+"/api/auth/login", map[string]string{"username": user, "password": pass}, http.StatusOK)
		return c, res
	}

	// A family account sets it up for themselves.
	postJSON[map[string]any](t, admin, base+"/api/users", map[string]any{"username": "kim", "password": "kim-password-1", "role": "member"}, http.StatusCreated)
	kim, res := login("kim", "kim-password-1")
	if res["twoStep"] != nil {
		t.Fatalf("asked for a code before it was set up: %v", res)
	}
	st := getJSON[map[string]any](t, kim, base+"/api/auth/two-step")
	if st["enabled"] != false {
		t.Fatalf("status: %v", st)
	}
	setup := postJSON[map[string]any](t, kim, base+"/api/auth/two-step/setup", nil, http.StatusOK)
	secret := strings.ReplaceAll(setup["secret"].(string), " ", "")
	if len(secret) != 32 || !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/Mediarium:kim?") || len(setup["qr"].([]any)) < 21 {
		t.Fatalf("setup: %v", setup)
	}
	postJSON[map[string]any](t, kim, base+"/api/auth/two-step/enable", map[string]string{"code": "000000"}, http.StatusBadRequest)
	code, _ := totp.Code(secret, time.Now())
	on := postJSON[map[string]any](t, kim, base+"/api/auth/two-step/enable", map[string]string{"code": code}, http.StatusOK)
	recovery := on["recoveryCodes"].([]any)
	if len(recovery) != 10 {
		t.Fatalf("recovery codes: %v", on)
	}
	postJSON[map[string]any](t, kim, base+"/api/auth/two-step/setup", nil, http.StatusConflict) // already on

	// Now the password alone only gets as far as the code.
	fresh, res := login("kim", "kim-password-1")
	challenge, _ := res["challenge"].(string)
	if res["twoStep"] != true || challenge == "" {
		t.Fatalf("no code asked for: %v", res)
	}
	if status, _ := doStatus(t, fresh, http.MethodGet, base+"/api/auth/me"); status != http.StatusUnauthorized {
		t.Fatalf("signed in before the code: %d", status)
	}
	postJSON[map[string]any](t, fresh, base+"/api/auth/login/code", map[string]string{"challenge": challenge, "code": "000000"}, http.StatusUnauthorized)
	// The code used to switch it on can't be used again; the next one can.
	postJSON[map[string]any](t, fresh, base+"/api/auth/login/code", map[string]string{"challenge": challenge, "code": code}, http.StatusUnauthorized)
	next, _ := totp.Code(secret, time.Now().Add(30*time.Second))
	me := postJSON[map[string]any](t, fresh, base+"/api/auth/login/code", map[string]string{"challenge": challenge, "code": next}, http.StatusOK)
	if me["username"] != "kim" {
		t.Fatalf("sign-in: %v", me)
	}
	if status, _ := doStatus(t, fresh, http.MethodGet, base+"/api/auth/me"); status != http.StatusOK {
		t.Fatalf("not signed in after the code: %d", status)
	}
	// The challenge is used up.
	postJSON[map[string]any](t, fresh, base+"/api/auth/login/code", map[string]string{"challenge": challenge, "code": next}, http.StatusUnauthorized)

	// A recovery code works once.
	rc := recovery[0].(string)
	viaRecovery, res := login("kim", "kim-password-1")
	ok := postJSON[map[string]any](t, viaRecovery, base+"/api/auth/login/code", map[string]string{"challenge": res["challenge"].(string), "recoveryCode": strings.ToUpper(rc)}, http.StatusOK)
	if ok["recoveryLeft"] != float64(9) {
		t.Fatalf("recovery sign-in: %v", ok)
	}
	again, res := login("kim", "kim-password-1")
	postJSON[map[string]any](t, again, base+"/api/auth/login/code", map[string]string{"challenge": res["challenge"].(string), "recoveryCode": rc}, http.StatusUnauthorized)

	// Switching it off needs the password.
	postJSON[map[string]any](t, kim, base+"/api/auth/two-step/disable", map[string]string{"password": "wrong-password"}, http.StatusBadRequest)
	postJSON[map[string]any](t, kim, base+"/api/auth/two-step/disable", map[string]string{"password": "kim-password-1"}, http.StatusOK)
	if _, res := login("kim", "kim-password-1"); res["twoStep"] != nil {
		t.Fatalf("still asked for a code after switching it off: %v", res)
	}

	// An administrator can switch it off for someone who lost their phone.
	setup = postJSON[map[string]any](t, kim, base+"/api/auth/two-step/setup", nil, http.StatusOK)
	secret = strings.ReplaceAll(setup["secret"].(string), " ", "")
	code, _ = totp.Code(secret, time.Now())
	postJSON[map[string]any](t, kim, base+"/api/auth/two-step/enable", map[string]string{"code": code}, http.StatusOK)
	users := getJSON[[]map[string]any](t, admin, base+"/api/users")
	var kimID any
	for _, u := range users {
		if u["username"] == "kim" {
			kimID = u["id"]
		}
	}
	if status, _ := doStatus(t, kim, http.MethodDelete, fmt.Sprintf("%s/api/users/%v/two-step", base, kimID)); status != http.StatusForbidden {
		t.Fatalf("a member switched it off through the admin route: %d", status)
	}
	if status, _ := doStatus(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/users/%v/two-step", base, kimID)); status != http.StatusOK {
		t.Fatalf("admin switch-off: %d", status)
	}
	if _, res := login("kim", "kim-password-1"); res["twoStep"] != nil {
		t.Fatalf("still asked for a code after the admin switched it off: %v", res)
	}
}

func TestTwoStepSetupRefusesAPIKeys(t *testing.T) {
	_, base, admin := loginNewServer(t)
	key := postJSON[map[string]any](t, admin, base+"/api/auth/api-keys", map[string]string{"name": "script"}, http.StatusCreated)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/auth/two-step/setup", nil)
	req.Header.Set("X-API-Key", key["key"].(string))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an API key started two-step setup: %d", resp.StatusCode)
	}
}

func TestTwoStepTooManyTries(t *testing.T) {
	_, base, admin := loginNewServer(t)
	setup := postJSON[map[string]any](t, admin, base+"/api/auth/two-step/setup", nil, http.StatusOK)
	secret := strings.ReplaceAll(setup["secret"].(string), " ", "")
	code, _ := totp.Code(secret, time.Now())
	postJSON[map[string]any](t, admin, base+"/api/auth/two-step/enable", map[string]string{"code": code}, http.StatusOK)
	jar, _ := cookiejar.New(nil)
	tries := &http.Client{Jar: jar}
	res := postJSON[map[string]any](t, tries, base+"/api/auth/login", map[string]string{"username": "ryan", "password": "correct-horse-battery-staple"}, http.StatusOK)

	// Too many wrong codes end the challenge (and the sign-in limit for the
	// address may step in first): even the right code is refused afterwards.
	ch := res["challenge"].(string)
	codeStatus := func(c string) int {
		body := strings.NewReader(fmt.Sprintf(`{"challenge":%q,"code":%q}`, ch, c))
		resp, err := tries.Post(base+"/api/auth/login/code", "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for range 5 {
		if s := codeStatus("111111"); s != http.StatusUnauthorized && s != http.StatusTooManyRequests {
			t.Fatalf("a wrong code: %d", s)
		}
	}
	later, _ := totp.Code(secret, time.Now().Add(60*time.Second))
	if s := codeStatus(later); s == http.StatusOK {
		t.Fatal("the right code still worked after too many wrong ones")
	}

}

// A right password must not hand out fresh guesses: signing in again after
// each wrong code doesn't reset the count, so guessing the code runs out.
func TestTwoStepGuessesRunOutAcrossSignIns(t *testing.T) {
	_, base, admin := loginNewServer(t)
	setup := postJSON[map[string]any](t, admin, base+"/api/auth/two-step/setup", nil, http.StatusOK)
	secret := strings.ReplaceAll(setup["secret"].(string), " ", "")
	code, _ := totp.Code(secret, time.Now())
	postJSON[map[string]any](t, admin, base+"/api/auth/two-step/enable", map[string]string{"code": code}, http.StatusOK)

	jar, _ := cookiejar.New(nil)
	attacker := &http.Client{Jar: jar}
	signIn := func() (int, string) {
		body := strings.NewReader(`{"username":"ryan","password":"correct-horse-battery-staple"}`)
		resp, err := attacker.Post(base+"/api/auth/login", "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		ch, _ := out["challenge"].(string)
		return resp.StatusCode, ch
	}
	tryCode := func(ch, c string) int {
		resp, err := attacker.Post(base+"/api/auth/login/code", "application/json", strings.NewReader(fmt.Sprintf(`{"challenge":%q,"code":%q}`, ch, c)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	blocked := false
	for range 12 {
		status, ch := signIn()
		if status == http.StatusTooManyRequests {
			blocked = true
			break
		}
		if s := tryCode(ch, "111111"); s == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("twelve rounds of password plus a wrong code were never slowed down")
	}
}
