package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomicNormal 常规路径：应走 rename 原子替换，不触发回退。
func TestWriteFileAtomicNormal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	fallback, err := WriteFileAtomic(path, []byte("new-content"), 0o600)
	if err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if fallback {
		t.Error("常规目录下不应触发回退路径")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-content" {
		t.Errorf("内容 = %q, want %q", got, "new-content")
	}
	// 临时文件必须被清理干净。
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("临时文件未被清理")
	}
}

// TestWriteFileAtomicCreatesNew 目标不存在时应能创建。
func TestWriteFileAtomicCreatesNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "new.json")
	if _, err := WriteFileAtomic(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Errorf("内容 = %q", got)
	}
}

// TestWriteFileAtomicPermission 权限应为 0600（凭证/配置含敏感信息）。
func TestWriteFileAtomicPermission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	if _, err := WriteFileAtomic(path, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("权限 = %o, want 600", perm)
	}
}

// TestOverwriteInPlace 直接验证回退实现本身（模拟 rename 不可用的场景）。
func TestOverwriteInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mounted.json")
	if err := os.WriteFile(path, []byte("original-longer-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 写更短的内容：必须完整截断，不能残留旧内容尾巴。
	if err := overwriteInPlace(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("overwriteInPlace: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "short" {
		t.Errorf("内容 = %q, want %q（截断不彻底会残留旧数据）", got, "short")
	}
}

// TestIsBindMountRenameErr 判定逻辑：EBUSY/EXDEV 才算挂载问题。
func TestIsBindMountRenameErr(t *testing.T) {
	if isBindMountRenameErr(nil) {
		t.Error("nil 不应判为挂载错误")
	}
	if isBindMountRenameErr(os.ErrNotExist) {
		t.Error("ENOENT 不应判为挂载错误")
	}
}

func TestDirOf(t *testing.T) {
	cases := map[string]string{
		"/a/b/c.json": "/a/b",
		"a.json":      ".",
		"/a.json":     "/",
	}
	for in, want := range cases {
		if got := dirOf(in); got != want {
			t.Errorf("dirOf(%q) = %q, want %q", in, got, want)
		}
	}
}
