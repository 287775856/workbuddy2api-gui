package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbuddy2api-gui/internal/config"
)

// newTestStore 构造会话表用于测试。
func newTestStore() *SessionStore { return NewSessionStore(time.Hour) }

func testConfig() *config.Config {
	c := config.Default()
	c.UI.Username = "admin"
	c.UI.Password = "secret123"
	c.UI.TTL = time.Hour
	return c
}

// TestCreateValidatesCredentials 正确/错误口令。
func TestCreateValidatesCredentials(t *testing.T) {
	s := newTestStore()
	cfg := testConfig()

	if _, ok, _ := s.Create("admin", "wrong", cfg, "1.1.1.1"); ok {
		t.Error("错误口令不应创建会话")
	}
	if _, ok, _ := s.Create("nobody", "secret123", cfg, "1.1.1.1"); ok {
		t.Error("错误用户名不应创建会话")
	}
	token, ok, _ := s.Create("admin", "secret123", cfg, "1.1.1.1")
	if !ok || token == "" {
		t.Fatal("正确口令应创建会话")
	}
	if user, valid := s.Validate(token); !valid || user != "admin" {
		t.Errorf("会话校验失败: user=%q valid=%v", user, valid)
	}
}

// TestBruteForceLockout 连续失败达上限后锁定同一来源。
func TestBruteForceLockout(t *testing.T) {
	s := newTestStore()
	cfg := testConfig()
	src := "9.9.9.9"

	for i := 0; i < maxLoginFailures; i++ {
		s.Create("admin", "bad", cfg, src)
	}
	// 第 N+1 次即使口令正确也应被锁定拒绝。
	_, ok, msg := s.Create("admin", "secret123", cfg, src)
	if ok {
		t.Fatal("达到失败上限后应锁定该来源")
	}
	if !strings.Contains(msg, "失败次数过多") {
		t.Errorf("应提示锁定原因, 得到 %q", msg)
	}
	// 其他来源不受影响（避免一个 IP 拖垮全站登录）。
	if _, ok, _ := s.Create("admin", "secret123", cfg, "8.8.8.8"); !ok {
		t.Error("其他来源不应被连带锁定")
	}
}

// TestSuccessResetsFailures 登录成功应清空失败计数。
func TestSuccessResetsFailures(t *testing.T) {
	s := newTestStore()
	cfg := testConfig()
	src := "7.7.7.7"

	for i := 0; i < maxLoginFailures-1; i++ {
		s.Create("admin", "bad", cfg, src)
	}
	if _, ok, _ := s.Create("admin", "secret123", cfg, src); !ok {
		t.Fatal("未达上限时应允许登录")
	}
	// 计数已清空：再次连续失败 maxLoginFailures-1 次仍不应锁定。
	for i := 0; i < maxLoginFailures-1; i++ {
		s.Create("admin", "bad", cfg, src)
	}
	if _, ok, _ := s.Create("admin", "secret123", cfg, src); !ok {
		t.Error("成功登录后失败计数应已清零")
	}
}

// TestExpiredSession 过期会话应失效。
func TestExpiredSession(t *testing.T) {
	s := NewSessionStore(time.Millisecond)
	cfg := testConfig()
	token, ok, _ := s.Create("admin", "secret123", cfg, "1.1.1.1")
	if !ok {
		t.Fatal("创建会话失败")
	}
	time.Sleep(5 * time.Millisecond)
	if _, valid := s.Validate(token); valid {
		t.Error("过期会话不应通过校验")
	}
}

// TestRevoke 注销后 token 失效。
func TestRevoke(t *testing.T) {
	s := newTestStore()
	cfg := testConfig()
	token, _, _ := s.Create("admin", "secret123", cfg, "1.1.1.1")
	s.Revoke(token)
	if _, valid := s.Validate(token); valid {
		t.Error("注销后 token 应失效")
	}
}

// TestValidateEmptyAndUnknown 空/伪造 token 一律拒绝。
func TestValidateEmptyAndUnknown(t *testing.T) {
	s := newTestStore()
	if _, ok := s.Validate(""); ok {
		t.Error("空 token 不应通过")
	}
	if _, ok := s.Validate("deadbeefdeadbeef"); ok {
		t.Error("伪造 token 不应通过")
	}
}

// TestSameOriginOrigin 校验逻辑。
func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"http://127.0.0.1:8787", "127.0.0.1:8787", true},
		{"https://gui.example.com", "gui.example.com", true},
		{"http://127.0.0.1:8787/", "127.0.0.1:8787", true},
		{"https://evil.example.com", "127.0.0.1:8787", false},
		{"http://127.0.0.1:9999", "127.0.0.1:8787", false},
		{"null", "127.0.0.1:8787", false},
	}
	for _, c := range cases {
		if got := sameOrigin(c.origin, c.host); got != c.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}

// TestClientSource 来源解析（IP 提取）。
func TestClientSource(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	if got := clientSource(r); got != "203.0.113.5" {
		t.Errorf("clientSource = %q, want 203.0.113.5", got)
	}
	// 无端口的 RemoteAddr：原样返回，不应 panic。
	r.RemoteAddr = "203.0.113.5"
	if got := clientSource(r); got != "203.0.113.5" {
		t.Errorf("clientSource = %q", got)
	}
}

// TestNewSessionStoreDefaultTTL 非正 TTL 应回落默认值（避免会话永不过期）。
func TestNewSessionStoreDefaultTTL(t *testing.T) {
	s := NewSessionStore(0)
	if s.ttl <= 0 {
		t.Errorf("TTL 应回落正值, 得到 %v", s.ttl)
	}
}
