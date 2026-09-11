package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaults 校验默认值符合部署预期。
func TestDefaults(t *testing.T) {
	c := Default()
	if c.Listen != ":8787" {
		t.Errorf("默认端口 = %q, want :8787（不能与网关 7863 冲突）", c.Listen)
	}
	if c.GatewayURL != "http://127.0.0.1:7863" {
		t.Errorf("默认网关地址 = %q", c.GatewayURL)
	}
	// 高危操作与只读必须默认关闭/关闭，避免默认状态下就能删账号。
	if c.DangerousOps {
		t.Error("dangerous_ops 应默认 false")
	}
	if c.ReadOnly {
		t.Error("read_only 应默认 false")
	}
	if c.BackupDir == "" {
		t.Error("backup_dir 应有默认值")
	}
}

// TestLoadFromFile 常规加载。
func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := map[string]any{
		"listen":        ":9000",
		"gateway_url":   "http://gateway:7863/", // 尾部斜杠应被裁掉
		"dangerous_ops": true,
		"ui":            map[string]any{"username": "u", "password": "p", "session_ttl": "30m"},
	}
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Listen != ":9000" {
		t.Errorf("listen = %q", c.Listen)
	}
	if c.GatewayURL != "http://gateway:7863" {
		t.Errorf("gateway_url 尾斜杠未裁剪: %q", c.GatewayURL)
	}
	if !c.DangerousOps {
		t.Error("dangerous_ops 未生效")
	}
	if c.UI.TTL.Minutes() != 30 {
		t.Errorf("session_ttl 解析错误: %v", c.UI.TTL)
	}
}

// TestLoadMissingFile 文件不存在时用默认可跑（首次启动体验）。
func TestLoadMissingFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("文件缺失不应报错: %v", err)
	}
	if c.Listen != ":8787" {
		t.Errorf("listen = %q", c.Listen)
	}
}

// TestRejectGatewayConfig 关键防呆：把网关 config.json 当面板配置加载必须被拒绝，
// 否则面板会静默监听到网关端口、并用错凭证目录（真实踩过的坑）。
func TestRejectGatewayConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	gatewayCfg := map[string]any{
		"listen":   ":7863",
		"api_key":  "x",
		"auth_dir": "./auths",
		"schedule": map[string]any{"checkin_hours": []int{9, 21}},
		"pool":     map[string]any{"max_in_flight": 3},
		"cooldown": map[string]any{"soft_rate": "600s"},
		"upstream": map[string]any{"timeout_seconds": 120},
	}
	raw, _ := json.Marshal(gatewayCfg)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("把网关配置当面板配置加载时应报错，但成功加载了")
	}
	// 错误信息要给出可操作的指引，而不只是"解析失败"。
	for _, want := range []string{"workbuddy2api", "config_file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺少指引 %q: %v", want, err)
		}
	}
}

// TestNormalizeListen 纯端口号应自动补冒号。
func TestNormalizeListen(t *testing.T) {
	c := &Config{}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8787" {
		t.Errorf("空 listen 应回落默认: %q", c.Listen)
	}
	c2 := &Config{Listen: "9000"}
	if err := c2.normalize(); err != nil {
		t.Fatal(err)
	}
	if c2.Listen != ":9000" {
		t.Errorf("纯端口号未补冒号: %q", c2.Listen)
	}
}

// TestUsingDefaultPassword 默认口令检测（供前端弹安全提示）。
func TestUsingDefaultPassword(t *testing.T) {
	c := Default()
	if !c.UsingDefaultPassword() {
		t.Error("默认配置应报告使用了默认口令")
	}
	c.UI.Password = "strong-password"
	if c.UsingDefaultPassword() {
		t.Error("改过口令后不应再报告默认口令")
	}
}

// TestEnvOverride 环境变量覆盖。
func TestEnvOverride(t *testing.T) {
	t.Setenv("WBGUI_LISTEN", ":9111")
	t.Setenv("WBGUI_DANGEROUS_OPS", "true")
	t.Setenv("WBGUI_PASSWORD", "envpass")

	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":9111" {
		t.Errorf("listen = %q, want :9111", c.Listen)
	}
	if !c.DangerousOps {
		t.Error("dangerous_ops 未被环境变量覆盖")
	}
	if c.UI.Password != "envpass" {
		t.Errorf("password = %q", c.UI.Password)
	}
}

// TestInvalidSessionTTL 非法 session_ttl 应报错而非静默忽略。
func TestInvalidSessionTTL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, _ := json.Marshal(map[string]any{"ui": map[string]any{"session_ttl": "not-a-duration"}})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("非法 session_ttl 应报错")
	}
}
