package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/discovery"
	"mcp-gateway-adapter/internal/registry"
)

// ServerConfig holds the parameters for running the Admin web console.
type ServerConfig struct {
	Host         string
	Port         int
	AllowedHosts []string
	SecretDir    string
	Store        *registry.Store
	Core         *core.Core
	Discovery    *discovery.TargetDiscovery
	Version      string
}

// Server implements the complete Go-only Admin Web console.
type Server struct {
	cfg           ServerConfig
	mux           *http.ServeMux
	templates     *pongo2.TemplateSet
	sessionMgr    *SessionManager
	loginLimiter  *LoginLimiter
	gatewayPubKey string
	mu            sync.Mutex
}

// NewServer initializes a new Admin Web Server with all routes and middleware.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}
	if len(cfg.AllowedHosts) == 0 {
		cfg.AllowedHosts = []string{"127.0.0.1", "localhost", cfg.Host}
	}
	if cfg.Discovery == nil {
		cfg.Discovery = discovery.NewTargetDiscovery("")
	}
	if cfg.Version == "" {
		cfg.Version = "1.4.0"
	}

	sessionMgr, err := NewSessionManager(cfg.SecretDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize session manager: %w", err)
	}

	templates := NewTemplateSet()
	pubKey := readGatewayPublicKey()

	s := &Server{
		cfg:           cfg,
		mux:           http.NewServeMux(),
		templates:     templates,
		sessionMgr:    sessionMgr,
		loginLimiter:  NewLoginLimiter(),
		gatewayPubKey: pubKey,
	}

	s.registerRoutes()
	return s, nil
}

func readGatewayPublicKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519.pub"),
		filepath.Join(home, ".ssh", "id_rsa.pub"),
		"/home/mcp-gateway/.ssh/id_ed25519.pub",
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// Handler returns the root HTTP handler wrapped in security middleware.
func (s *Server) Handler() http.Handler {
	return s.securityMiddleware(s.mux)
}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host header validation (prevent DNS rebinding)
		host := r.Host
		if colon := strings.LastIndex(host, ":"); colon != -1 {
			host = host[:colon]
		}
		host = strings.Trim(host, "[]")
		host = strings.ToLower(strings.TrimSpace(host))

		allowed := false
		for _, h := range s.cfg.AllowedHosts {
			cleanH := strings.ToLower(strings.TrimSpace(h))
			if cleanH == "*" || cleanH == host {
				allowed = true
				break
			}
		}
		if !allowed {
			http.Error(w, "Untrusted Host header", http.StatusForbidden)
			return
		}

		// 2. Strict Security Headers
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none';")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")

		// 3. Load Session from Cookie
		sess := s.sessionMgr.GetSession(r)
		if sess == nil {
			sess = &SessionData{
				CSRFToken: GenerateCSRFToken(),
			}
		} else if sess.Username != "" && time.Since(sess.LastActive) > SessionTimeout {
			sess = &SessionData{
				CSRFToken: GenerateCSRFToken(),
			}
			sess.Flash("Session expired due to inactivity. Please log in again.", "warning")
		} else if sess.Username != "" {
			sess.LastActive = time.Now()
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, sess)
		recW := &sessionResponseWriter{
			ResponseWriter: w,
			sess:           sess,
			server:         s,
			req:            r,
		}

		// 4. CSRF Check on mutating methods (POST, PUT, DELETE, PATCH)
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH" {
			if !strings.HasPrefix(r.URL.Path, "/static/") {
				_ = r.ParseForm()
				token := r.FormValue("csrf_token")
				if token == "" {
					token = r.Header.Get("X-CSRF-Token")
				}
				if !ValidateCSRFToken(token, sess.CSRFToken) {
					http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
					return
				}
			}
		}

		next.ServeHTTP(recW, r.WithContext(ctx))
	})
}

type sessionContextKeyType struct{}

var sessionContextKey = sessionContextKeyType{}

func getSession(r *http.Request) *SessionData {
	if val := r.Context().Value(sessionContextKey); val != nil {
		if sess, ok := val.(*SessionData); ok {
			return sess
		}
	}
	return &SessionData{CSRFToken: GenerateCSRFToken()}
}

type sessionResponseWriter struct {
	http.ResponseWriter
	sess   *SessionData
	server *Server
	req    *http.Request
	wrote  bool
}

func (w *sessionResponseWriter) WriteHeader(status int) {
	if !w.wrote {
		if w.sess != nil {
			_ = w.server.sessionMgr.SetCookie(w.ResponseWriter, w.req, *w.sess)
		}
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *sessionResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		if w.sess != nil {
			_ = w.server.sessionMgr.SetCookie(w.ResponseWriter, w.req, *w.sess)
		}
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Serve starts listening and serving HTTP traffic.
func (s *Server) Serve(listener net.Listener) error {
	server := &http.Server{
		Handler:      s.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return server.Serve(listener)
}

// ListenAndServe binds to Host:Port and serves traffic.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

// RecordAudit logs an activity event to the registry store.
func (s *Server) RecordAudit(r *http.Request, action, targetID, projectID string, success bool, errorCode, detail string, required bool) bool {
	sess := getSession(r)
	actor := sess.Username
	if actor == "" {
		actor = "system"
	}
	var tID *string
	if targetID != "" {
		tID = &targetID
	}
	var pID *string
	if projectID != "" {
		pID = &projectID
	}
	var errCode *string
	if errorCode != "" {
		errCode = &errorCode
	}
	entry := registry.Activity{
		Actor:     actor,
		Action:    action,
		TargetID:  tID,
		ProjectID: pID,
		Success:   success,
		ErrorCode: errCode,
		Detail:    detail,
	}
	err := s.cfg.Store.RecordActivity(r.Context(), entry)
	if err != nil {
		if required {
			panic(fmt.Sprintf("AUDIT_UNAVAILABLE: %v", err))
		}
		return false
	}
	return true
}

func toTemplateData(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case string, int, int32, int64, float32, float64, bool:
		return v
	case func() string, func() []map[string]string:
		return v
	case []string:
		return v
	case map[string]string:
		return v
	case pongo2.Context:
		return v
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return v
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			return v
		}
		return out
	}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, templateName string, ctx pongo2.Context) {
	sess := getSession(r)

	if ctx == nil {
		ctx = pongo2.Context{}
	}

	flashes := sess.PopFlashes()
	var flashList []map[string]string
	for _, f := range flashes {
		flashList = append(flashList, map[string]string{
			"category": f.Category,
			"text":     f.Message,
		})
	}

	// Convert data structures to template-friendly map representation (snake_case from json tags)
	for k, v := range ctx {
		if k == "session" || k == "csrf_token" || k == "get_flashed_messages" || k == "request" {
			continue
		}
		ctx[k] = toTemplateData(v)
	}

	ctx["session"] = map[string]interface{}{
		"user": sess.Username,
	}
	ctx["csrf_token"] = func() string {
		return sess.CSRFToken
	}
	ctx["get_flashed_messages"] = func() []map[string]string {
		return flashList
	}
	ctx["request"] = map[string]interface{}{
		"host":      r.Host,
		"path":      r.URL.Path,
		"full_path": r.URL.RequestURI(),
	}

	tpl, err := s.templates.FromFile(templateName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
		return
	}

	out, err := tpl.Execute(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Render error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := getSession(r)
		if sess.Username == "" {
			nextURL := r.URL.RequestURI()
			http.Redirect(w, r, URLFor("auth.login", nextURL), http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) registerRoutes() {
	// Static assets from embed.FS
	subFS, err := fs.Sub(staticFS, "static")
	if err == nil {
		s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(subFS))))
	}

	// Auth routes
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)

	// Dashboard & Main
	s.mux.HandleFunc("/", s.requireAuth(s.handleIndex))
	s.mux.HandleFunc("/dashboard", s.requireAuth(s.handleDashboard))

	// Targets
	s.mux.HandleFunc("/targets", s.requireAuth(s.handleTargetsList))
	s.mux.HandleFunc("/targets/add", s.requireAuth(s.handleTargetAdd))
	s.mux.HandleFunc("/targets/", s.requireAuth(s.handleTargetSubroutes))

	// Projects
	s.mux.HandleFunc("/projects", s.requireAuth(s.handleProjectsList))
	s.mux.HandleFunc("/projects/add", s.requireAuth(s.handleProjectAdd))
	s.mux.HandleFunc("/projects/", s.requireAuth(s.handleProjectSubroutes))

	// Clients
	s.mux.HandleFunc("/clients", s.requireAuth(s.handleClientsList))
	s.mux.HandleFunc("/clients/add", s.requireAuth(s.handleClientAdd))
	s.mux.HandleFunc("/clients/", s.requireAuth(s.handleClientSubroutes))

	// Activity
	s.mux.HandleFunc("/activity", s.requireAuth(s.handleActivity))

	// Settings
	s.mux.HandleFunc("/settings", s.requireAuth(s.handleSettings))
	s.mux.HandleFunc("/settings/kill-switch", s.requireAuth(s.handleSettingsKillSwitch))
	s.mux.HandleFunc("/settings/toggle-writes", s.requireAuth(s.handleSettingsToggleWrites))
	s.mux.HandleFunc("/settings/disable-writes", s.requireAuth(s.handleSettingsDisableWrites))
	s.mux.HandleFunc("/settings/toggle-shell", s.requireAuth(s.handleSettingsToggleShell))

	// Maintenance
	s.mux.HandleFunc("/maintenance", s.requireAuth(s.handleMaintenance))
	s.mux.HandleFunc("/maintenance/doctor", s.requireAuth(s.handleMaintenanceDoctor))
	s.mux.HandleFunc("/maintenance/backup", s.requireAuth(s.handleMaintenanceBackup))
	s.mux.HandleFunc("/maintenance/repair", s.requireAuth(s.handleMaintenanceRepair))
	s.mux.HandleFunc("/maintenance/rollback", s.requireAuth(s.handleMaintenanceRollback))

	// System
	s.mux.HandleFunc("/system", s.requireAuth(s.handleSystem))
}
