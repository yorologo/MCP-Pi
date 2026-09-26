package admin

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckPasswordHashWerkzeugScryptFixture(t *testing.T) {
	fixture := "scrypt:32768:8:1$nFy8eiK0G3rKDNsU$51db85365a7e71dd90777ec22ed09546ca617183b35acbe30309f1d8f32fe937622af6310423ebb6bf55dcc65ed792a36d48b27798228d5b36034c2c69792f9a"
	password := "correcthorsebatterystaple"

	if !CheckPasswordHash(fixture, password) {
		t.Fatal("failed to verify Werkzeug scrypt hash fixture with correct password")
	}

	if CheckPasswordHash(fixture, "wrongpassword") {
		t.Fatal("wrong password unexpectedly passed Werkzeug scrypt hash check")
	}
}

func TestGenerateAndCheckPasswordHashRoundtrip(t *testing.T) {
	password := "SecretP@ssw0rd!"
	hash, err := GeneratePasswordHash(password)
	if err != nil {
		t.Fatalf("GeneratePasswordHash failed: %v", err)
	}

	if !CheckPasswordHash(hash, password) {
		t.Fatal("failed to verify newly generated password hash")
	}

	if CheckPasswordHash(hash, "wrong") {
		t.Fatal("wrong password unexpectedly passed hash check")
	}
}

func TestCheckPasswordHashPBKDF2(t *testing.T) {
	hash := "pbkdf2:sha256:1000$5006Kbx8aYlejJim$78ecbb10bd70f8f0423992f77b4c090c40e1dc207e77f8299a88def532aae45a"
	password := "pbkdf2pass"

	if !CheckPasswordHash(hash, password) {
		t.Fatal("failed to verify Werkzeug PBKDF2 hash with correct password")
	}
	if CheckPasswordHash(hash, "wrong") {
		t.Fatal("wrong password unexpectedly passed PBKDF2 check")
	}
}

func TestLoginLimiter(t *testing.T) {
	limiter := NewLoginLimiter()
	key := "admin-user"

	for i := 0; i < 4; i++ {
		limiter.RecordFailure(key)
		if limiter.IsRateLimited(key) {
			t.Fatalf("unexpected rate limit before threshold on attempt %d", i+1)
		}
	}

	// 5th failure triggers lockout
	limiter.RecordFailure(key)
	if !limiter.IsRateLimited(key) {
		t.Fatal("expected rate limit lockout after 5 failures")
	}

	// Reset clears lockout
	limiter.ResetFailures(key)
	if limiter.IsRateLimited(key) {
		t.Fatal("rate limit should be cleared after reset")
	}
}

func TestSessionManager(t *testing.T) {
	secretDir := t.TempDir()
	sm, err := NewSessionManager(secretDir)
	if err != nil {
		t.Fatalf("NewSessionManager failed: %v", err)
	}

	csrf := GenerateCSRFToken()
	data := SessionData{
		Username:   "admin",
		CSRFToken:  csrf,
		LastActive: time.Now(),
	}

	encoded, err := sm.Encode(data)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := sm.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if decoded.Username != "admin" || decoded.CSRFToken != csrf {
		t.Fatalf("decoded session mismatch: %+v", decoded)
	}

	// Verify key persistence: new session manager with same secret dir can decode
	sm2, err := NewSessionManager(secretDir)
	if err != nil {
		t.Fatalf("NewSessionManager 2 failed: %v", err)
	}
	decoded2, err := sm2.Decode(encoded)
	if err != nil || decoded2.Username != "admin" {
		t.Fatalf("second session manager failed to decode with persisted secret: %v, %+v", err, decoded2)
	}

	// Test cookie handling
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	if err := sm.SetCookie(rec, req, data); err != nil {
		t.Fatalf("SetCookie failed: %v", err)
	}

	cookie := rec.Result().Cookies()[0]
	if cookie.Name != DefaultSessionCookie || !cookie.HttpOnly {
		t.Fatalf("unexpected cookie attributes: %+v", cookie)
	}

	// Test CSRF token validation
	if !ValidateCSRFToken(csrf, csrf) {
		t.Fatal("valid CSRF token rejected")
	}
	if ValidateCSRFToken(csrf, "wrong-token") {
		t.Fatal("invalid CSRF token accepted")
	}
}
