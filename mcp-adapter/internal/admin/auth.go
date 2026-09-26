package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/securecookie"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

const (
	SessionTimeout          = 30 * time.Minute
	MaxLoginFailedAttempts  = 5
	LoginFailureWindow      = 5 * time.Minute
	LoginLockoutDuration    = 1 * time.Minute
	DefaultSessionCookie    = "mcp_admin_session"
)

// CheckPasswordHash verifies passwords formatted by Werkzeug (scrypt:32768:8:1$... or pbkdf2:sha256:...).
func CheckPasswordHash(passwordHash, password string) bool {
	parts := strings.Split(passwordHash, "$")
	if len(parts) != 3 {
		return false
	}
	methodParams := strings.Split(parts[0], ":")
	salt := []byte(parts[1])
	expectedHex := parts[2]

	switch methodParams[0] {
	case "scrypt":
		if len(methodParams) != 4 {
			return false
		}
		N, err := strconv.Atoi(methodParams[1])
		if err != nil {
			return false
		}
		r, err := strconv.Atoi(methodParams[2])
		if err != nil {
			return false
		}
		p, err := strconv.Atoi(methodParams[3])
		if err != nil {
			return false
		}
		keyLen := len(expectedHex) / 2
		if keyLen <= 0 {
			return false
		}
		dk, err := scrypt.Key([]byte(password), salt, N, r, p, keyLen)
		if err != nil {
			return false
		}
		actualHex := hex.EncodeToString(dk)
		return subtle.ConstantTimeCompare([]byte(actualHex), []byte(expectedHex)) == 1

	case "pbkdf2":
		if len(methodParams) != 3 {
			return false
		}
		iter, err := strconv.Atoi(methodParams[2])
		if err != nil {
			return false
		}
		keyLen := len(expectedHex) / 2
		if keyLen <= 0 {
			return false
		}
		var h func() hash.Hash
		switch methodParams[1] {
		case "sha256":
			h = sha256.New
		case "sha512":
			h = sha512.New
		default:
			return false
		}
		dk := pbkdf2.Key([]byte(password), salt, iter, keyLen, h)
		actualHex := hex.EncodeToString(dk)
		return subtle.ConstantTimeCompare([]byte(actualHex), []byte(expectedHex)) == 1
	}

	return false
}

// GeneratePasswordHash creates a Werkzeug-compatible scrypt:32768:8:1 password hash.
func GeneratePasswordHash(password string) (string, error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", err
	}
	salt := hex.EncodeToString(saltBytes)
	dk, err := scrypt.Key([]byte(password), []byte(salt), 32768, 8, 1, 64)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt:32768:8:1$%s$%s", salt, hex.EncodeToString(dk)), nil
}

type loginFailureRecord struct {
	Count        int
	LastTime     time.Time
	LockoutUntil time.Time
}

type LoginLimiter struct {
	mu      sync.Mutex
	records map[string]loginFailureRecord
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{records: make(map[string]loginFailureRecord)}
}

func (l *LoginLimiter) IsRateLimited(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	rec, exists := l.records[key]
	if !exists {
		return false
	}
	if now.Before(rec.LockoutUntil) {
		return true
	}
	if now.Sub(rec.LastTime) > LoginFailureWindow {
		delete(l.records, key)
		return false
	}
	return false
}

func (l *LoginLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	rec, exists := l.records[key]
	if !exists || now.Sub(rec.LastTime) > LoginFailureWindow {
		l.records[key] = loginFailureRecord{Count: 1, LastTime: now}
		return
	}
	rec.Count++
	rec.LastTime = now
	if rec.Count >= MaxLoginFailedAttempts {
		rec.LockoutUntil = now.Add(LoginLockoutDuration)
	}
	l.records[key] = rec
}

func (l *LoginLimiter) ResetFailures(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, key)
}

// VerifyPassword is an alias for CheckPasswordHash.
func VerifyPassword(passwordHash, password string) bool {
	return CheckPasswordHash(passwordHash, password)
}

type FlashMessage struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

type SessionData struct {
	Username   string                 `json:"username"`
	CSRFToken  string                 `json:"csrf_token"`
	LastActive time.Time              `json:"last_active"`
	Flashes    []FlashMessage         `json:"flashes,omitempty"`
	Pending    map[string]interface{} `json:"pending,omitempty"`
}

func (s *SessionData) Flash(msg, category string) {
	if s == nil {
		return
	}
	s.Flashes = append(s.Flashes, FlashMessage{
		Category: category,
		Message:  msg,
	})
}

func (s *SessionData) PopFlashes() []FlashMessage {
	if s == nil {
		return nil
	}
	f := s.Flashes
	s.Flashes = nil
	return f
}

type SessionManager struct {
	sc         *securecookie.SecureCookie
	secretPath string
}

func NewSessionManager(secretDir string) (*SessionManager, error) {
	if secretDir == "" {
		home := os.Getenv("MCP_GATEWAY_HOME")
		if home == "" {
			home = "/home/mcp-gateway"
		}
		secretDir = filepath.Join(home, ".config", "mcp-gateway")
	}
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		return nil, fmt.Errorf("create secret directory: %w", err)
	}
	secretFile := filepath.Join(secretDir, "admin_session_secret")

	var hashKey, blockKey []byte
	if data, err := os.ReadFile(secretFile); err == nil && len(data) == 64 {
		hashKey = data[:32]
		blockKey = data[32:64]
	} else {
		hashKey = securecookie.GenerateRandomKey(32)
		blockKey = securecookie.GenerateRandomKey(32)
		if hashKey == nil || blockKey == nil {
			return nil, errors.New("failed to generate secure cookie keys")
		}
		combined := append(hashKey, blockKey...)
		_ = os.WriteFile(secretFile, combined, 0o600)
	}

	sc := securecookie.New(hashKey, blockKey)
	sc.MaxAge(int(SessionTimeout.Seconds()))
	return &SessionManager{sc: sc, secretPath: secretFile}, nil
}

func (sm *SessionManager) Encode(data SessionData) (string, error) {
	return sm.sc.Encode(DefaultSessionCookie, data)
}

func (sm *SessionManager) Decode(cookieValue string) (*SessionData, error) {
	var data SessionData
	if err := sm.sc.Decode(DefaultSessionCookie, cookieValue, &data); err != nil {
		return nil, err
	}
	if time.Since(data.LastActive) > SessionTimeout {
		return nil, errors.New("session expired")
	}
	return &data, nil
}

func (sm *SessionManager) SetCookie(w http.ResponseWriter, r *http.Request, data SessionData) error {
	data.LastActive = time.Now()
	encoded, err := sm.Encode(data)
	if err != nil {
		return err
	}
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     DefaultSessionCookie,
		Value:    encoded,
		Path:     "/",
		MaxAge:   int(SessionTimeout.Seconds()),
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (sm *SessionManager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     DefaultSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (sm *SessionManager) GetSession(r *http.Request) *SessionData {
	c, err := r.Cookie(DefaultSessionCookie)
	if err != nil {
		return nil
	}
	data, err := sm.Decode(c.Value)
	if err != nil {
		return nil
	}
	return data
}

func GenerateCSRFToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ValidateCSRFToken(token, expected string) bool {
	if token == "" || expected == "" {
		return false
	}
	return hmac.Equal([]byte(token), []byte(expected))
}
