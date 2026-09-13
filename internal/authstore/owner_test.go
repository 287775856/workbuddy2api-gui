package authstore

import (
	"os"
	"syscall"
	"testing"
)

// TestSetOwnerApplies 落盘后应把凭证文件属主改成配置的 uid/gid。
//
// 背景：容器部署时面板以 root 写、网关以低权限用户（10001）读，
// 若文件是 root:0600 网关就读不到，表现为「账号已添加但池中未加载」。
func TestSetOwnerApplies(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("需要 root 才能测试 chown")
	}
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	const uid, gid = 10001, 10001
	s.SetOwner(uid, gid)

	a := &Account{UID: "uid-owner-test-1", AccessToken: "at", RefreshToken: "rt"}
	if err := s.Save(a); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := s.PathFor(a.UID)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("非 Unix 系统")
	}
	if int(sys.Uid) != uid || int(sys.Gid) != gid {
		t.Errorf("属主 = %d:%d, want %d:%d", sys.Uid, sys.Gid, uid, gid)
	}
	// 目录需可进入，否则网关无法 open 其中的文件。
	dst, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dst.Mode().Perm()&0o055 == 0 {
		t.Errorf("目录权限 %o 不允许其他用户进入", dst.Mode().Perm())
	}
}

// TestOwnerDefaultDisabled 未配置 SetOwner 时不应改动属主（兼容非容器场景）。
func TestOwnerDefaultDisabled(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := s.Owner()
	if uid != -1 || gid != -1 {
		t.Errorf("默认属主应为 -1/-1（不改），得到 %d/%d", uid, gid)
	}
	a := &Account{UID: "uid-default-owner", AccessToken: "at"}
	if err := s.Save(a); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 属主应保持当前进程用户，且不报错。
	st, err := os.Stat(s.PathFor(a.UID))
	if err != nil {
		t.Fatal(err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("非 Unix 系统")
	}
	if int(sys.Uid) != os.Geteuid() {
		t.Errorf("未配置时不应改属主: got %d, want %d", sys.Uid, os.Geteuid())
	}
}

// TestPathFor 非法 uid 返回空串（防路径穿越）。
func TestPathFor(t *testing.T) {
	s, _ := New(t.TempDir())
	if p := s.PathFor("../../etc/passwd"); p != "" {
		t.Errorf("非法 uid 应返回空串，得到 %q", p)
	}
	if p := s.PathFor("valid-uid-123456"); p == "" {
		t.Error("合法 uid 应返回路径")
	}
}
