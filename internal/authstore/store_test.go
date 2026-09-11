package authstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestParseNested 插件 OAuth 的嵌套格式。
func TestParseNested(t *testing.T) {
	raw := []byte(`{
	  "account": {"uid":"uid-1","enterpriseId":"ent-1","nickname":"昵称"},
	  "auth": {"accessToken":"at","refreshToken":"rt","expiresAt":1794289203,"domain":"copilot.tencent.com"}
	}`)
	a, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.UID != "uid-1" || a.AccessToken != "at" || a.RefreshToken != "rt" {
		t.Errorf("解析结果错误: %+v", a)
	}
	if a.Nickname != "昵称" || a.EnterpriseID != "ent-1" {
		t.Errorf("账号字段错误: %+v", a)
	}
	if a.Domain != "copilot.tencent.com" || a.ExpiresAt != 1794289203 {
		t.Errorf("auth 字段错误: %+v", a)
	}
}

// TestParseFlat 兼容手写/旧版扁平格式。
func TestParseFlat(t *testing.T) {
	raw := []byte(`{"accessToken":"at","refreshToken":"rt","uid":"u","nickname":"n","expiresAt":100}`)
	a, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.UID != "u" || a.AccessToken != "at" {
		t.Errorf("扁平格式解析错误: %+v", a)
	}
}

// TestParseRejectsEmptyToken 无 accessToken 的凭证必须被拒绝。
func TestParseRejectsEmptyToken(t *testing.T) {
	for _, raw := range []string{`{}`, `{"account":{"uid":"u"}}`, `{"auth":{"accessToken":"  "}}`, ``} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("应拒绝无效凭证: %q", raw)
		}
	}
}

// TestValidUID 路径穿越防护：uid 会拼进文件名，必须严格校验。
func TestValidUID(t *testing.T) {
	valid := []string{
		"9a7b3c21-4d5e-4f60-8a91-2b3c4d5e6f70",
		"abc123",
		"user.name_1-2",
	}
	for _, u := range valid {
		if !ValidUID(u) {
			t.Errorf("ValidUID(%q) = false, want true", u)
		}
	}
	invalid := []string{
		"../../etc/passwd",
		"..",
		"a/b",
		"a\\b",
		"",
		"short", // 少于 6 字符
		"has space",
		"has\x00null",
		"中文uid",
	}
	for _, u := range invalid {
		if ValidUID(u) {
			t.Errorf("ValidUID(%q) = true, want false", u)
		}
	}
}

// TestSaveAndGetRoundTrip 落盘后应能原样读回，且权限为 0600。
func TestSaveAndGetRoundTrip(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Account{
		UID:          "uid-roundtrip-1234",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		Domain:       "copilot.tencent.com",
		Nickname:     "测试",
		EnterpriseID: "ent",
	}
	if err := s.Save(a); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(a.UID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != a.AccessToken || got.RefreshToken != a.RefreshToken {
		t.Errorf("token 不一致: %+v", got)
	}
	if got.Nickname != "测试" || got.EnterpriseID != "ent" {
		t.Errorf("账号字段不一致: %+v", got)
	}

	// 权限必须是 0600（凭证含敏感 token）。
	st, err := os.Stat(got.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("凭证文件权限 = %o, want 600", perm)
	}

	// 文件名格式需与网关的扫描规则（workbuddy*.json）一致。
	if base := filepath.Base(got.FilePath); base != "workbuddy-"+a.UID+".json" {
		t.Errorf("文件名 = %q", base)
	}
}

// TestSaveWritesNestedShape 写回必须是网关/插件都能读的嵌套形态。
func TestSaveWritesNestedShape(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	a := &Account{UID: "uid-shape-test", AccessToken: "at", RefreshToken: "rt", Domain: "d"}
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "workbuddy-uid-shape-test.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if _, ok := probe["auth"]; !ok {
		t.Error("缺少 auth 段")
	}
	if _, ok := probe["account"]; !ok {
		t.Error("缺少 account 段")
	}
}

// TestSaveRefusesEmptyToken 空 accessToken 绝不能覆盖有效凭证文件。
func TestSaveRefusesEmptyToken(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	good := &Account{UID: "uid-guard-test", AccessToken: "valid", RefreshToken: "rt"}
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}

	bad := &Account{UID: "uid-guard-test", AccessToken: "", FilePath: good.FilePath}
	if err := s.Save(bad); err == nil {
		t.Fatal("空 accessToken 应被拒绝写入")
	}
	// 原文件必须完好。
	got, err := s.Get("uid-guard-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "valid" {
		t.Errorf("原凭证被破坏: %q", got.AccessToken)
	}
}

// TestSaveRefusesTraversalUID 非法 uid 不得写出目录之外。
func TestSaveRefusesTraversalUID(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	a := &Account{UID: "../../escaped", AccessToken: "at"}
	if err := s.Save(a); err == nil {
		t.Fatal("非法 uid 应被拒绝")
	}
}

// TestListSkipsBadFiles 坏文件不应中断列表，需在 warnings 中报告。
func TestListSkipsBadFiles(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	if err := s.Save(&Account{UID: "uid-good-123456", AccessToken: "at", Nickname: "好账号"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workbuddy-broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workbuddy-emptytoken.json"), []byte(`{"auth":{"accessToken":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	list, warnings := s.List()
	if len(list) != 1 {
		t.Errorf("应只解析出 1 个有效账号, 得到 %d", len(list))
	}
	if len(warnings) != 2 {
		t.Errorf("应报告 2 个问题文件, 得到 %d: %v", len(warnings), warnings)
	}
}

// TestDeleteAndMissing 删除与「无凭证文件」的区分。
func TestDeleteAndMissing(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	if err := s.Save(&Account{UID: "uid-del-123456", AccessToken: "at"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("uid-del-123456"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("uid-del-123456"); !os.IsNotExist(err) {
		t.Errorf("删除后应读不到, 得到 %v", err)
	}
	// 再删应返回专门的哨兵错误（供 API 给出可读提示）。
	if err := s.Delete("uid-del-123456"); err != ErrNoCredentialFile {
		t.Errorf("重复删除应返回 ErrNoCredentialFile, 得到 %v", err)
	}
}

// TestNeedsRefresh 过期判定。
func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		expiresAt int64
		within    time.Duration
		want      bool
	}{
		{"已过期", now.Add(-time.Hour).Unix(), 10 * time.Minute, true},
		{"即将过期", now.Add(5 * time.Minute).Unix(), 10 * time.Minute, true},
		{"仍然有效", now.Add(time.Hour).Unix(), 10 * time.Minute, false},
		{"无过期时间", 0, 10 * time.Minute, true},
	}
	for _, c := range cases {
		a := &Account{ExpiresAt: c.expiresAt}
		if got := a.NeedsRefresh(c.within); got != c.want {
			t.Errorf("%s: NeedsRefresh = %v, want %v", c.name, got, c.want)
		}
	}
}
