package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workbuddy2api-gui/internal/config"
)

// newTestServer 构造一个带临时凭据文件的 Server，用于改密码端点的测试。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.UI.Username = "admin"
	cfg.UI.Password = "oldpass123"
	cfg.UI.TTL = 0
	cfg.CredentialsFile = filepath.Join(dir, "credentials.json")

	s := NewServer(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), "test")
	return s
}

// doChangePassword 通过 handler 执行一次改密码请求并返回结果。
func doChangePassword(t *testing.T, s *Server, cookie *http.Cookie, current, next, newUser string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"current_password": current,
		"new_password":     next,
		"new_username":     newUser,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/password", strings.NewReader(string(body)))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

// TestChangePasswordFlow 完整流程：登录 → 改密码 → 旧口令失效 → 新口令可登录。
func TestChangePasswordFlow(t *testing.T) {
	s := newTestServer(t)

	// 1. 先登录拿 Cookie。
	token, ok, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	if !ok {
		t.Fatal("初始登录失败")
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}

	// 2. 用正确旧口令改密码。
	w, resp := doChangePassword(t, s, cookie, "oldpass123", "newpass456", "")
	if w.Code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("改密码应成功: code=%d resp=%v", w.Code, resp)
	}

	// 3. 旧口令登录应失败。
	if _, ok, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1"); ok {
		t.Error("旧口令不应再能登录")
	}
	// 4. 新口令登录应成功。
	if _, ok, _ := s.sessions.Create("admin", "newpass456", s.cfg, "1.1.1.1"); !ok {
		t.Error("新口令应能登录")
	}
	// 5. 内存配置已更新。
	if s.cfg.UI.Password != "newpass456" {
		t.Errorf("内存口令未更新: %q", s.cfg.UI.Password)
	}
}

// TestChangePasswordWrongCurrent 错误旧口令必须被拒绝。
func TestChangePasswordWrongCurrent(t *testing.T) {
	s := newTestServer(t)
	token, _, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}

	w, resp := doChangePassword(t, s, cookie, "WRONG", "newpass456", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("错误旧口令应 403, 得到 %d", w.Code)
	}
	if resp["code"] != "wrong_password" {
		t.Errorf("应返回 wrong_password 码: %v", resp)
	}
	// 口令未变。
	if s.cfg.UI.Password != "oldpass123" {
		t.Errorf("错误旧口令不应改口令: %q", s.cfg.UI.Password)
	}
}

// TestChangePasswordShortNew 新口令太短应被拒绝。
func TestChangePasswordShortNew(t *testing.T) {
	s := newTestServer(t)
	token, _, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}

	w, resp := doChangePassword(t, s, cookie, "oldpass123", "123", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("短口令应 400, 得到 %d", w.Code)
	}
	if !strings.Contains(resp["error"].(string), "6") {
		t.Errorf("应提示长度要求: %v", resp)
	}
}

// TestChangePasswordUsername 可同时改用户名。
func TestChangePasswordUsername(t *testing.T) {
	s := newTestServer(t)
	token, _, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}

	_, resp := doChangePassword(t, s, cookie, "oldpass123", "newpass456", "boss")
	if resp["ok"] != true {
		t.Fatalf("改用户名应成功: %v", resp)
	}
	if s.cfg.UI.Username != "boss" {
		t.Errorf("用户名未更新: %q", s.cfg.UI.Username)
	}
	// 新用户名 + 新口令可登录。
	if _, ok, _ := s.sessions.Create("boss", "newpass456", s.cfg, "1.1.1.1"); !ok {
		t.Error("新用户名+新口令应能登录")
	}
}

// TestChangePasswordPersists 改密码后凭据应落盘，重启（重新 Load）后仍生效。
func TestChangePasswordPersists(t *testing.T) {
	s := newTestServer(t)
	token, _, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}

	_, resp := doChangePassword(t, s, cookie, "oldpass123", "persisted999", "")
	if resp["ok"] != true {
		t.Fatalf("改密码失败: %v", resp)
	}

	// 模拟重启：从同一凭据文件重新加载。
	cfg2 := config.Default()
	cfg2.UI.Password = "oldpass123"
	cfg2.CredentialsFile = s.cfg.CredentialsFile
	cfg2.LoadStoredCredentials()
	if cfg2.UI.Password != "persisted999" {
		t.Errorf("重启后口令未持久化: %q", cfg2.UI.Password)
	}
}

// TestChangePasswordNotSupported 未配置 credentials_file 时应明确报错。
func TestChangePasswordNotSupported(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Password = "oldpass123"
	cfg.CredentialsFile = "" // 未配置
	s := NewServer(cfg, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), "test")
	token, _, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}

	w, resp := doChangePassword(t, s, cookie, "oldpass123", "newpass456", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未配置应 400, 得到 %d", w.Code)
	}
	if resp["code"] != "not_supported" {
		t.Errorf("应返回 not_supported: %v", resp)
	}
}

// TestChangePasswordRequiresAuth 未登录调用改密码应 401。
func TestChangePasswordRequiresAuth(t *testing.T) {
	s := newTestServer(t)
	w, _ := doChangePassword(t, s, nil, "oldpass123", "newpass456", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401, 得到 %d", w.Code)
	}
}

// TestCredentialFilePermission 凭据文件权限应为 0600。
func TestCredentialFilePermission(t *testing.T) {
	s := newTestServer(t)
	token, _, _ := s.sessions.Create("admin", "oldpass123", s.cfg, "1.1.1.1")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}
	doChangePassword(t, s, cookie, "oldpass123", "newpass456", "")

	st, err := os.Stat(s.cfg.CredentialsFile)
	if err != nil {
		t.Fatalf("凭据文件应存在: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("凭据文件权限 = %o, want 600", perm)
	}
}
