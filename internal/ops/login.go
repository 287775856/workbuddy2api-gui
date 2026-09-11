// login.go 网页版 OAuth 设备授权登录：把 login.sh 的多账号登录流程搬进 GUI。
//
// 流程（与 cmd/login 一致，无 PKCE）：
//
//	① Start   → POST /v2/plugin/auth/state 拿 state + authUrl，创建会话
//	② 用户在浏览器打开 authUrl 完成登录
//	③ Poll    → GET /v2/plugin/auth/token?state= 轮询；成功后自动拉取 uid/nickname
//	④ Finish  → 落盘 auths/workbuddy-<uid>.json，可选重启网关容器加载
package ops

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"workbuddy2api-gui/internal/authstore"
	"workbuddy2api-gui/internal/upstream"
)

// LoginState 登录会话当前阶段。
type LoginState string

const (
	// LoginPending 已生成授权 URL，等待用户在浏览器完成登录。
	LoginPending LoginState = "pending"
	// LoginSuccess 已成功拿到凭证。
	LoginSuccess LoginState = "success"
	// LoginError 登录过程出错。
	LoginError LoginState = "error"
	// LoginExpired 会话超过 TTL 未完成。
	LoginExpired LoginState = "expired"
	// LoginCancelled 用户主动取消。
	LoginCancelled LoginState = "cancelled"
)

// loginSessionTTL 登录会话存活时长（超过即视为过期，避免 state 长期滞留）。
const loginSessionTTL = 15 * time.Minute

// LoginSession 一次设备授权登录会话。
type LoginSession struct {
	ID        string          `json:"id"`
	State     string          `json:"-"`      // 上游 state：不下发给前端，避免被误用
	Region    upstream.Region `json:"region"` // cn | global
	AuthURL   string          `json:"auth_url"`
	Status    LoginState      `json:"status"`
	Message   string          `json:"message,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`

	// 成功后填充。
	UID      string `json:"uid,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Saved    bool   `json:"saved"` // 凭证是否已落盘
	File     string `json:"file,omitempty"`
	Restart  string `json:"restart,omitempty"` // 容器重启结果说明

	// 已弹过的过期提醒只发一次，避免前端重复提示。
	notifiedExpiry bool

	// acct 登录成功后暂存的凭证，等待 Service 落盘后清除（不下发前端）。
	acct *authstore.Account
}

// LoginManager 管理进行中的登录会话。
type LoginManager struct {
	up *upstream.Client

	mu       sync.Mutex
	sessions map[string]*LoginSession
	// activeID 当前进行中的登录会话 id（同一时间只允许一个，避免多账号登录串会话）。
	activeID string
}

// NewLoginManager 构建登录管理器。
func NewLoginManager(up *upstream.Client) *LoginManager {
	return &LoginManager{up: up, sessions: map[string]*LoginSession{}}
}

// Start 发起一次登录：申请 state 与授权 URL，返回会话快照。
func (m *LoginManager) Start(region upstream.Region) (*LoginSession, error) {
	state, authURL, err := m.up.StartLogin(region)
	if err != nil {
		return nil, fmt.Errorf("申请授权失败: %w", err)
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sess := &LoginSession{
		ID:        id,
		State:     state,
		Region:    region,
		AuthURL:   authURL,
		Status:    LoginPending,
		Message:   "请在浏览器中打开授权链接并完成登录，然后点击「我已完成登录」",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	// 清理过期会话，避免 map 无界增长。
	m.gcLocked(now)
	m.sessions[id] = sess
	m.activeID = id
	m.mu.Unlock()
	return m.snapshot(sess), nil
}

// Poll 轮询一次登录状态；成功则落盘并按配置重启容器。
func (m *LoginManager) Poll(id string) (*LoginSession, error) {
	sess, err := m.get(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	switch sess.Status {
	case LoginSuccess, LoginError, LoginCancelled:
		out := m.snapshotLocked(sess)
		m.mu.Unlock()
		return out, nil
	case LoginExpired:
		out := m.snapshotLocked(sess)
		m.mu.Unlock()
		return out, nil
	}
	if time.Since(sess.CreatedAt) > loginSessionTTL {
		sess.Status = LoginExpired
		sess.Message = "登录会话已超时（15 分钟），请重新发起登录"
		sess.UpdatedAt = time.Now()
		out := m.snapshotLocked(sess)
		m.mu.Unlock()
		return out, nil
	}
	state := sess.State
	region := sess.Region
	m.mu.Unlock()

	acct, err := m.up.PollLogin(region, state)
	if err != nil {
		m.mu.Lock()
		sess.Status = LoginError
		sess.Message = err.Error()
		sess.UpdatedAt = time.Now()
		out := m.snapshotLocked(sess)
		m.mu.Unlock()
		return out, nil
	}
	if acct == nil {
		// 尚未完成登录：保持 pending，前端继续轮询。
		m.mu.Lock()
		sess.Message = "等待授权完成…（请在浏览器中完成登录）"
		sess.UpdatedAt = time.Now()
		out := m.snapshotLocked(sess)
		m.mu.Unlock()
		return out, nil
	}

	// 成功：暂存凭证，落盘与容器重启由 Service.CompleteLogin 完成。
	m.mu.Lock()
	sess.UID = acct.UID
	sess.Nickname = acct.Nickname
	sess.Status = LoginSuccess
	sess.Message = "登录成功，正在保存凭证…"
	sess.acct = acct
	sess.UpdatedAt = time.Now()
	out := m.snapshotLocked(sess)
	m.mu.Unlock()
	return out, nil
}

// takeAccount 取出并清除登录成功后暂存的凭证（保证只被落盘一次）。
func (m *LoginManager) takeAccount(id string) *authstore.Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok || sess.acct == nil {
		return nil
	}
	acct := sess.acct
	sess.acct = nil
	return acct
}

// Get 返回会话快照。
func (m *LoginManager) Get(id string) (*LoginSession, error) {
	sess, err := m.get(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	// 惰性过期：读取时若已超时且仍在等待，翻转为 expired。
	if sess.Status == LoginPending && time.Since(sess.CreatedAt) > loginSessionTTL {
		sess.Status = LoginExpired
		sess.Message = "登录会话已超时（15 分钟），请重新发起登录"
		sess.UpdatedAt = time.Now()
	}
	out := m.snapshotLocked(sess)
	m.mu.Unlock()
	return out, nil
}

// Cancel 取消一个登录会话。
func (m *LoginManager) Cancel(id string) (*LoginSession, error) {
	sess, err := m.get(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if sess.Status == LoginPending {
		sess.Status = LoginCancelled
		sess.Message = "已取消登录"
		sess.UpdatedAt = time.Now()
	}
	if m.activeID == id {
		m.activeID = ""
	}
	out := m.snapshotLocked(sess)
	m.mu.Unlock()
	return out, nil
}

// Current 返回当前进行中的会话（没有则 nil）。
func (m *LoginManager) Current() *LoginSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID == "" {
		return nil
	}
	sess, ok := m.sessions[m.activeID]
	if !ok || sess.Status != LoginPending {
		return nil
	}
	out := m.snapshotLocked(sess)
	return out
}

// markSaved 记录凭证落盘与重启结果（由 Service 调用）。
func (m *LoginManager) markSaved(id, file, restart string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[id]; ok {
		sess.Saved = true
		sess.File = file
		sess.Restart = restart
		sess.UpdatedAt = time.Now()
	}
	if m.activeID == id {
		m.activeID = ""
	}
}

// get 取会话。
func (m *LoginManager) get(id string) (*LoginSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("登录会话不存在或已过期")
	}
	return sess, nil
}

// gcLocked 清理超过 TTL 的终态会话。调用方必须已持锁。
func (m *LoginManager) gcLocked(now time.Time) {
	for id, sess := range m.sessions {
		if sess.Status == LoginPending {
			continue
		}
		if now.Sub(sess.UpdatedAt) > loginSessionTTL {
			delete(m.sessions, id)
		}
	}
}

// snapshot 生成会话快照（加锁版本）。
func (m *LoginManager) snapshot(sess *LoginSession) *LoginSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(sess)
}

// snapshotLocked 拷贝一份会话，避免把内部指针暴露给调用方并发改写。
// 调用方必须已持锁。
func (m *LoginManager) snapshotLocked(sess *LoginSession) *LoginSession {
	cp := *sess
	return &cp
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成会话 id 失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}
