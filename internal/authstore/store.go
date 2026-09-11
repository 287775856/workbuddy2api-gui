// Package authstore 读取/写入 workbuddy2api 的账号凭证文件（auths/workbuddy-<uid>.json）。
//
// 磁盘格式与 workbuddy2api/internal/auth 完全一致（嵌套形）：
//
//	{
//	  "account": {"uid","enterpriseId","nickname"},
//	  "auth":    {"accessToken","refreshToken","expiresAt","domain"}
//	}
//
// 兼容读取扁平形（手写/旧版）。写回一律用嵌套形，保证网关与插件都能读。
package authstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"workbuddy2api-gui/internal/fsutil"
)

// Account 归一化后的账号凭证。
type Account struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // Unix 秒
	Domain       string `json:"domain"`

	UID          string `json:"uid"`
	EnterpriseID string `json:"enterpriseId"`
	Nickname     string `json:"nickname"`

	// FilePath 来源文件绝对路径；仅进程内使用，不序列化到凭证文件。
	FilePath string `json:"-"`
}

// ExpiresAtTime 返回过期时刻。
func (a *Account) ExpiresAtTime() time.Time { return time.Unix(a.ExpiresAt, 0) }

// NeedsRefresh 报告 token 是否将在 within 内过期或已过期。
func (a *Account) NeedsRefresh(within time.Duration) bool {
	if a.ExpiresAt <= 0 {
		return true
	}
	return time.Now().Add(within).Unix() >= a.ExpiresAt
}

// Expired 报告 token 是否已过期。
func (a *Account) Expired() bool { return a.ExpiresAt > 0 && time.Now().Unix() >= a.ExpiresAt }

// uidPattern 校验 uid 形态：UUID 或安全字符集。
// 用于文件名拼接，必须拒绝路径分隔符与 ..，防止穿越写入 auths 目录之外。
var uidPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{6,128}$`)

// ValidUID 报告 uid 是否可安全用于文件名。
func ValidUID(uid string) bool {
	return uidPattern.MatchString(uid) && !strings.Contains(uid, "..")
}

// Store 账号凭证目录访问器。并发安全：所有写操作在进程内互斥。
type Store struct {
	dir string
	mu  sync.Mutex
}

// New 构建 Store；dir 为空时报错（凭证目录是必需配置）。
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("账号凭证目录（auth_dir）未配置")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("解析凭证目录: %w", err)
	}
	return &Store{dir: abs}, nil
}

// Dir 返回凭证目录绝对路径。
func (s *Store) Dir() string { return s.dir }

// pathFor 返回 uid 对应的凭证文件绝对路径（已校验 uid，无穿越风险）。
func (s *Store) pathFor(uid string) (string, error) {
	if !ValidUID(uid) {
		return "", fmt.Errorf("非法 uid：%q", uid)
	}
	return filepath.Join(s.dir, "workbuddy-"+uid+".json"), nil
}

// List 扫描目录下全部 workbuddy-*.json 并按 uid 排序返回。
// 单个文件解析失败不中断：跳过并在第二个返回值里报告，让前端能看到坏文件。
func (s *Store) List() ([]*Account, []string) {
	files, err := filepath.Glob(filepath.Join(s.dir, "workbuddy*.json"))
	if err != nil {
		return nil, []string{fmt.Sprintf("扫描目录失败: %v", err)}
	}
	sort.Strings(files)
	out := make([]*Account, 0, len(files))
	var warnings []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: 读取失败 %v", filepath.Base(f), err))
			continue
		}
		a, err := Parse(raw)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		a.FilePath = f
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, warnings
}

// Get 按 uid 读取单个账号；不存在返回 os.ErrNotExist 包装错误。
func (s *Store) Get(uid string) (*Account, error) {
	p, err := s.pathFor(uid)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	a, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	a.FilePath = p
	return a, nil
}

// Parse 解析两种磁盘形态：嵌套形（插件 OAuth 输出）与扁平形（手写/旧版）。
func Parse(raw []byte) (*Account, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("空凭证文件")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("凭证 JSON 解析失败: %w", err)
	}
	var a Account
	if _, nested := probe["auth"]; nested {
		var n struct {
			Auth struct {
				AccessToken  string `json:"accessToken"`
				RefreshToken string `json:"refreshToken"`
				ExpiresAt    int64  `json:"expiresAt"`
				Domain       string `json:"domain"`
			} `json:"auth"`
			Account struct {
				UID          string `json:"uid"`
				EnterpriseID string `json:"enterpriseId"`
				Nickname     string `json:"nickname"`
			} `json:"account"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("凭证 JSON 解析失败: %w", err)
		}
		a = Account{
			AccessToken:  n.Auth.AccessToken,
			RefreshToken: n.Auth.RefreshToken,
			ExpiresAt:    n.Auth.ExpiresAt,
			Domain:       n.Auth.Domain,
			UID:          n.Account.UID,
			EnterpriseID: n.Account.EnterpriseID,
			Nickname:     n.Account.Nickname,
		}
	} else {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("凭证 JSON 解析失败: %w", err)
		}
	}
	if strings.TrimSpace(a.AccessToken) == "" {
		return nil, fmt.Errorf("缺少 accessToken")
	}
	return &a, nil
}

// Save 原子写回账号凭证（嵌套形，0600 权限）。
// accessToken 为空时拒绝写入——避免用半更新凭证覆盖有效文件。
func (s *Store) Save(a *Account) error {
	if strings.TrimSpace(a.AccessToken) == "" {
		return fmt.Errorf("拒绝写入：accessToken 为空")
	}
	if !ValidUID(a.UID) {
		return fmt.Errorf("拒绝写入：非法 uid %q", a.UID)
	}
	p := a.FilePath
	if p == "" {
		var err error
		if p, err = s.pathFor(a.UID); err != nil {
			return err
		}
		a.FilePath = p
	}
	doc := map[string]any{
		"account": map[string]any{
			"uid":          a.UID,
			"enterpriseId": a.EnterpriseID,
			"nickname":     a.Nickname,
		},
		"auth": map[string]any{
			"accessToken":  a.AccessToken,
			"refreshToken": a.RefreshToken,
			"expiresAt":    a.ExpiresAt,
			"domain":       a.Domain,
		},
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeAtomic(p, raw)
}

// writeAtomic 原子替换（tmp + rename）；在 Docker 单文件挂载等无法 rename 的场景
// 自动回退为原地写入，避免"凭证刷新成功却存不下去"。
// 调用方必须已持有 s.mu。
func (s *Store) writeAtomic(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	if _, err := fsutil.WriteFileAtomic(path, raw, 0o600); err != nil {
		return err
	}
	return nil
}

// ErrNoCredentialFile 表示该账号没有本地凭证文件可删除。
var ErrNoCredentialFile = errors.New("该账号没有本地凭证文件（面板挂载的凭证目录可能与网关不一致）")

// Delete 删除账号凭证文件。
func (s *Store) Delete(uid string) error {
	p, err := s.pathFor(uid)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			// 常见于「账号在网关池中但本地没有凭证文件」：凭证目录与网关不一致，
			// 例如面板挂载的 auths/ 不是网关容器挂载的那个目录。
			return ErrNoCredentialFile
		}
		return err
	}
	return nil
}

// Rename 把 oldUID 的凭证文件改名为 newUID（用于修正 uid）。
func (s *Store) Rename(oldUID, newUID string) error {
	oldPath, err := s.pathFor(oldUID)
	if err != nil {
		return err
	}
	newPath, err := s.pathFor(newUID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("目标账号 %s 已存在", newUID)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	return nil
}
