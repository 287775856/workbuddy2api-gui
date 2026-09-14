package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStatsEndpointMissing 网关没有 /v1/stats 时（官方上游版本）应降级为
// Enabled=false + 可读说明，而不是抛 "HTTP 404" 让面板显示红色报错。
//
// 背景：/v1/stats 是本面板配套网关版本的扩展端点，官方上游（Sliverkiss）
// 版本并不提供。用户把面板指向官方网关时，统计页此前会恒报 404。
func TestStatsEndpointMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // 官方网关对未知路径的响应
	}))
	defer srv.Close()

	c := New(srv.URL, func() string { return "k" }, 5*time.Second)
	st, err := c.Stats(context.Background())
	if err != nil {
		t.Fatalf("404 应降级而非报错，得到: %v", err)
	}
	if st.Enabled {
		t.Error("Enabled 应为 false")
	}
	if st.Message == "" {
		t.Error("Message 应给出可读说明")
	}
	if !strings.Contains(st.Message, "/v1/stats") {
		t.Errorf("Message 应点明缺失的端点，得到 %q", st.Message)
	}
}

// TestStatsOK 正常网关仍按原样解析。
func TestStatsOK(t *testing.T) {
	body := `{"enabled":true,"uptime_sec":60,"models":[{"model":"m1","requests":2}],
	           "total":{"model":"(all)","requests":2}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, func() string { return "k" }, 5*time.Second)
	st, err := c.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if !st.Enabled {
		t.Error("Enabled 应为 true")
	}
	if len(st.Models) != 1 || st.Models[0].Model != "m1" {
		t.Errorf("models 解析错误: %+v", st.Models)
	}
	if st.Total.Requests != 2 {
		t.Errorf("total.requests = %d, want 2", st.Total.Requests)
	}
}

// TestStatsUnauthorized 401 仍应报错（与端点缺失区分开：前者是配置问题，需修）。
func TestStatsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, func() string { return "bad" }, 5*time.Second)
	if _, err := c.Stats(context.Background()); err == nil {
		t.Fatal("401 应返回错误")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("错误信息应提及 401，得到: %v", err)
	}
}

// TestStatsServerError 其余非 200（如 500）仍应报错，不被降级吞掉。
func TestStatsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := New(srv.URL, func() string { return "k" }, 5*time.Second)
	if _, err := c.Stats(context.Background()); err == nil {
		t.Fatal("500 应返回错误，不应降级")
	}
}

// TestStatsDisabledByConfig 网关启用了端点但关闭了开关（metrics_enabled=false）
// 时，应原样透传 enabled=false 与网关自己的说明，不被 404 分支覆盖。
func TestStatsDisabledByConfig(t *testing.T) {
	payload := map[string]any{
		"enabled": false,
		"message": "统计未启用（server.metrics_enabled=false）",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	c := New(srv.URL, func() string { return "k" }, 5*time.Second)
	st, err := c.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Enabled {
		t.Error("Enabled 应为 false")
	}
	// 关键：这是网关主动告知的关闭，信息应原样保留（含配置项名），
	// 不能被"端点缺失"的文案替换 —— 否则用户会去查错地方。
	if !strings.Contains(st.Message, "metrics_enabled") {
		t.Errorf("应保留网关原始说明，得到 %q", st.Message)
	}
}
