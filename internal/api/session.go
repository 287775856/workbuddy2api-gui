// session.go 面板自身的登录会话：内存态 token + HttpOnly Cookie。
//
// 为什么必须有鉴权：GUI 能读到账号 accessToken / refreshToken，等同账号完全控制权，
// 且支持改配置、重启容器。默认口令也强制存在，不允许无鉴权直通（见 config.UIConfig 注释）。
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"workbuddy2api-gui/internal/config"
)

const (
	sessionCookieName = "wbgui_session"

	// maxLoginFailures / loginLockWindow 暴力破解防护：同一来源连续失败达上限后锁定一段时间。
	maxLoginFailures = 10
	loginLockWindow  = 10 * time.Minute
)

// session 一条已登录会话。
type session struct {
	token     string
	username  string
	expiresAt time.Time
}

// loginAttempt 单个来源的失败计数。
type loginAttempt struct {
	failures int
	firstAt  time.Time
}

// SessionStore 内存会话表。
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
	attempts map[string]loginAttempt
	ttl      time.Duration
}

// NewSessionStore 构建会话表。
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &SessionStore{
		sessions: map[string]session{},
		attempts: map[string]loginAttempt{},
		ttl:      ttl,
	}
}

// Create 校验口令并创建会话；成功返回 token。
func (s *SessionStore) Create(username, password string, cfg *config.Config, source string) (string, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 先做锁定检查。
	if a, ok := s.attempts[source]; ok {
		if a.failures >= maxLoginFailures && time.Since(a.firstAt) < loginLockWindow {
			remain := (loginLockWindow - time.Since(a.firstAt)).Round(time.Second)
			return "", false, "失败次数过多，请 " + remain.String() + " 后再试"
		}
		if time.Since(a.firstAt) >= loginLockWindow {
			delete(s.attempts, source)
		}
	}

	// 常量时间比较，避免时序侧信道泄露口令长度/内容。
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(cfg.UI.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(cfg.UI.Password)) == 1
	if !userOK || !passOK {
		att := s.attempts[source]
		if att.failures == 0 || time.Since(att.firstAt) >= loginLockWindow {
			att = loginAttempt{firstAt: time.Now()}
		}
		att.failures++
		s.attempts[source] = att
		return "", false, "用户名或密码错误"
	}

	delete(s.attempts, source) // 成功即清空失败计数

	token, err := newToken()
	if err != nil {
		return "", false, "生成会话失败"
	}
	s.sessions[token] = session{token: token, username: username, expiresAt: time.Now().Add(s.ttl)}
	s.gcLocked()
	return token, true, ""
}

// Validate 校验 token 是否有效，返回用户名。
func (s *SessionStore) Validate(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(sess.expiresAt) {
		delete(s.sessions, token)
		return "", false
	}
	return sess.username, true
}

// Revoke 注销会话。
func (s *SessionStore) Revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// gcLocked 清理过期会话。调用方必须已持锁。
func (s *SessionStore) gcLocked() {
	now := time.Now()
	for t, sess := range s.sessions {
		if now.After(sess.expiresAt) {
			delete(s.sessions, t)
		}
	}
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// clientSource 取来源标识（IP），用于失败计数。
func clientSource(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ---------------------------------------------------------------------------
// 中间件
// ---------------------------------------------------------------------------

// authMiddleware 校验面板会话。放行：/api/login、/api/session、静态资源。
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只保护 /api/*；静态资源与 SPA 入口放行（前端首屏自行跳登录页）。
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/login", "/api/session":
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := s.currentUser(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "未登录或会话已过期",
				"code":  "unauthorized",
			})
			return
		}
		// 写操作的 CSRF 防线：带 Cookie 的请求要求同源。
		// SameSite=Strict 已挡住绝大多数跨站请求，这里再校验 Origin 作为纵深防御。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				if !sameOrigin(origin, r.Host) {
					writeJSON(w, http.StatusForbidden, map[string]any{
						"error": "跨站请求被拒绝",
						"code":  "csrf",
					})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// currentUser 从 Cookie 或 Authorization 头解析当前用户。
// 支持 Bearer 是为了让脚本 / curl 也能调用 API（自动化场景）。
func (s *Server) currentUser(r *http.Request) (string, bool) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if user, ok := s.sessions.Validate(c.Value); ok {
			return user, true
		}
	}
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		if user, ok := s.sessions.Validate(strings.TrimPrefix(authz, "Bearer ")); ok {
			return user, true
		}
	}
	return "", false
}

// sameOrigin 报告 Origin 是否与本机 Host 同源。
func sameOrigin(origin, host string) bool {
	origin = strings.TrimSuffix(origin, "/")
	for _, scheme := range []string{"http://", "https://"} {
		if strings.HasPrefix(origin, scheme) {
			return strings.EqualFold(strings.TrimPrefix(origin, scheme), host)
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 登录接口
// ---------------------------------------------------------------------------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin 面板登录。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
		return
	}
	token, ok, msg := s.sessions.Create(req.Username, req.Password, s.cfg, clientSource(r))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": msg})
		return
	}
	// SameSite=Strict 阻断跨站携带；Secure 交由部署方按 HTTPS 情况决定（本地 http 无法设 Secure）。
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.sessions.ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"username": req.Username,
	})
}

// handleLogout 注销。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.Revoke(c.Value)
	}
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		s.sessions.Revoke(strings.TrimPrefix(authz, "Bearer "))
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSessionInfo 返回当前会话与运行模式信息（前端首屏用，无需登录即可调用，
// 但未登录时不返回任何敏感信息）。
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	user, authed := s.currentUser(r)
	resp := map[string]any{
		"authenticated":          authed,
		"username":               user,
		"read_only":              s.cfg.ReadOnly,
		"dangerous_ops":          s.cfg.DangerousOps,
		"using_default_password": s.cfg.UsingDefaultPassword(),
		"gateway_url":            s.cfg.GatewayURL,
	}
	if !authed {
		// 未登录也透露「是否仍在用默认口令」是必要的：否则用户不知道去哪改。
		// 这里不含任何账号信息，风险可接受。
		resp["username"] = ""
	}
	writeJSON(w, http.StatusOK, resp)
}
