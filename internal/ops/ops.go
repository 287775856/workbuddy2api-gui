// Package ops GUI 的业务操作层：聚合网关状态与磁盘凭证，提供账号运维动作
// （签到 / 刷新 token / 猫猫旅行 / 积分查询）与批量任务。
package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"workbuddy2api-gui/internal/authstore"
	"workbuddy2api-gui/internal/config"
	"workbuddy2api-gui/internal/gateway"
	"workbuddy2api-gui/internal/upstream"
)

// 只读 / 高危开关相关的哨兵错误，供 API 层映射为合适的状态码。
var (
	// ErrReadOnly 服务端处于只读模式。
	ErrReadOnly = errors.New("服务端已开启只读模式，写操作被禁用")
	// ErrDangerousDisabled 高危操作未解锁。
	ErrDangerousDisabled = errors.New("该操作属高危动作，需在服务端配置开启 dangerous_ops 后才能执行")
)

// 批量任务里账号之间的间隔，避免对上游造成瞬时压力（与网关调度器口径对齐）。
const (
	checkinAccountDelay = 200 * time.Millisecond
	travelAccountDelay  = 800 * time.Millisecond
)

// AccountView 账号的合并视图：磁盘凭证 + 网关运行态 + 积分缓存。
type AccountView struct {
	UID          string `json:"uid"`
	Nickname     string `json:"nickname"`
	EnterpriseID string `json:"enterprise_id"`
	Domain       string `json:"domain"`

	HasFile      bool   `json:"has_file"`
	FileName     string `json:"file_name"`
	ExpiresAt    int64  `json:"expires_at"`
	Expired      bool   `json:"expired"`
	NeedsRefresh bool   `json:"needs_refresh"`
	RefreshToken bool   `json:"has_refresh_token"`

	// gateway 侧运行态；InGateway=false 表示该账号在池中不存在。
	InGateway       bool      `json:"in_gateway"`
	Status          string    `json:"status"` // healthy | cooling | disabled | unknown
	Cooling         bool      `json:"cooling"`
	CoolKind        string    `json:"cool_kind,omitempty"`
	CoolRemaining   int64     `json:"cool_remaining_sec,omitempty"`
	Disabled        bool      `json:"disabled"`
	Reason          string    `json:"reason,omitempty"`
	InFlight        int       `json:"in_flight"`
	BreakerFails    int       `json:"breaker_fails"`
	BreakerUntil    time.Time `json:"breaker_until,omitempty"`
	SoftStreak      int       `json:"soft_streak,omitempty"`
	SuccessCount    int64     `json:"success_count"`
	ErrTotal        int64     `json:"err_total"`
	LastSuccessTime time.Time `json:"last_success,omitempty"`
	LastErrTime     time.Time `json:"last_err,omitempty"`

	// 积分：GatewayCredits 来自 /status（分钟级刷新），LiveCredits 来自主动查询（更准）。
	GatewayCredits int64      `json:"credits"`
	LiveCredits    *int64     `json:"live_credits,omitempty"`
	CreditsAt      *time.Time `json:"credits_at,omitempty"`
}

// CreditsTotal 全局积分汇总。
type CreditsTotal struct {
	Remain   int64 `json:"remain"`
	Used     int64 `json:"used"`
	Size     int64 `json:"size"`
	Accounts int   `json:"accounts"`
	OK       int   `json:"ok"`
	Failed   int   `json:"failed"`
}

// Overview 仪表盘总览。
type Overview struct {
	GatewayURL     string          `json:"gateway_url"`
	GatewayOK      bool            `json:"gateway_ok"`
	GatewayError   string          `json:"gateway_error,omitempty"`
	Health         *gateway.Health `json:"health,omitempty"`
	Total          int             `json:"total"`
	Healthy        int             `json:"healthy"`
	Cooling        int             `json:"cooling"`
	Disabled       int             `json:"disabled"`
	InFlightFull   int             `json:"in_flight_full"`
	InFlight       int             `json:"in_flight"`
	StickySessions int             `json:"sticky_sessions"`
	RedisMode      string          `json:"redis_mode"`

	// 磁盘侧统计。
	FileCount  int      `json:"file_count"`
	Expired    int      `json:"expired"`
	Expiring   int      `json:"expiring"`
	Warnings   []string `json:"warnings,omitempty"`
	FileIssues []string `json:"file_issues,omitempty"`

	Credits CreditsTotal `json:"credits"`

	ReadOnly     bool `json:"read_only"`
	DangerousOps bool `json:"dangerous_ops"`

	ServerTime time.Time `json:"server_time"`
}

// OpResult 单账号操作结果。
type OpResult struct {
	UID     string         `json:"uid"`
	Action  string         `json:"action"`
	OK      bool           `json:"ok"`
	Message string         `json:"message"`
	Reward  int64          `json:"reward,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// creditCache 单账号积分缓存条目。
type creditCache struct {
	credits *upstream.Credits
	at      time.Time
	err     string
}

// Service 业务操作聚合入口。
type Service struct {
	cfg    *config.Config
	store  *authstore.Store
	gw     *gateway.Client
	up     *upstream.Client
	tasks  *TaskManager
	logins *LoginManager

	mu      sync.RWMutex
	credits map[string]creditCache
}

// New 构建服务。
func New(cfg *config.Config, store *authstore.Store, gw *gateway.Client, up *upstream.Client) *Service {
	return &Service{
		cfg:     cfg,
		store:   store,
		gw:      gw,
		up:      up,
		tasks:   NewTaskManager(),
		logins:  NewLoginManager(up),
		credits: map[string]creditCache{},
	}
}

// Config 返回当前配置。
func (s *Service) Config() *config.Config { return s.cfg }

// Tasks 返回任务管理器。
func (s *Service) Tasks() *TaskManager { return s.tasks }

// Logins 返回登录会话管理器。
func (s *Service) Logins() *LoginManager { return s.logins }

// Gateway 返回网关客户端。
func (s *Service) Gateway() *gateway.Client { return s.gw }

// Upstream 返回上游客户端。
func (s *Service) Upstream() *upstream.Client { return s.up }

// Store 返回凭证存储。
func (s *Service) Store() *authstore.Store { return s.store }

// EnsureWritable 写操作前置校验（导出供 API 层复用）。
func (s *Service) EnsureWritable() error { return s.ensureWritable() }

// ensureWritable 写操作前置校验。
func (s *Service) ensureWritable() error {
	if s.cfg.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

// ensureDangerous 高危操作前置校验。
func (s *Service) ensureDangerous() error {
	if err := s.ensureWritable(); err != nil {
		return err
	}
	if !s.cfg.DangerousOps {
		return ErrDangerousDisabled
	}
	return nil
}

// ---------------------------------------------------------------------------
// 账号视图聚合
// ---------------------------------------------------------------------------

// Accounts 返回「磁盘凭证 ∪ 网关运行态」的合并账号列表。
// 网关不可达时不报错：磁盘侧账号仍返回，状态标记为 unknown。
func (s *Service) Accounts(ctx context.Context) ([]AccountView, *gateway.Status, []string, error) {
	diskAccounts, fileIssues := s.store.List()
	status, gwErr := s.gw.Status(ctx)

	byUID := map[string]*AccountView{}
	for _, a := range diskAccounts {
		v := &AccountView{
			UID:          a.UID,
			Nickname:     a.Nickname,
			EnterpriseID: a.EnterpriseID,
			Domain:       a.Domain,
			HasFile:      true,
			FileName:     baseName(a.FilePath),
			ExpiresAt:    a.ExpiresAt,
			Expired:      a.Expired(),
			NeedsRefresh: a.NeedsRefresh(10 * time.Minute),
			RefreshToken: strings.TrimSpace(a.RefreshToken) != "",
			Status:       "unknown",
		}
		byUID[a.UID] = v
	}

	if status != nil {
		for i := range status.Accounts {
			ga := status.Accounts[i]
			v, ok := byUID[ga.UID]
			if !ok {
				// 池中存在但磁盘无凭证（凭证文件被删/损坏但池未重启）。
				v = &AccountView{UID: ga.UID, Status: "unknown"}
				byUID[ga.UID] = v
			}
			v.InGateway = true
			v.Cooling = ga.Cooling
			v.CoolKind = ga.CoolKind
			v.CoolRemaining = ga.CoolRemaining
			v.Disabled = ga.Disabled
			v.Reason = ga.Reason
			v.InFlight = ga.InFlight
			v.BreakerFails = ga.BreakerFails
			v.BreakerUntil = ga.BreakerUntil
			v.SoftStreak = ga.SoftStreak
			v.SuccessCount = ga.SuccessCount
			v.ErrTotal = ga.ErrTotal
			v.LastSuccessTime = ga.LastSuccessTime
			v.LastErrTime = ga.LastErrTime
			v.GatewayCredits = ga.Credits
			if v.Nickname == "" {
				v.Nickname = ga.Nickname
			}
			switch {
			case ga.Disabled:
				v.Status = "disabled"
			case ga.Cooling:
				v.Status = "cooling"
			default:
				v.Status = "healthy"
			}
		}
	}

	// 合并积分缓存。
	s.mu.RLock()
	for uid, c := range s.credits {
		if v, ok := byUID[uid]; ok && c.credits != nil {
			remain := c.credits.Remain
			at := c.at
			v.LiveCredits = &remain
			v.CreditsAt = &at
		}
	}
	s.mu.RUnlock()

	out := make([]AccountView, 0, len(byUID))
	for _, v := range byUID {
		if v.Status == "unknown" {
			// 磁盘有凭证但网关未见过（或网关不可达）：按 token 状态给个更友好的结论。
			if !v.HasFile {
				v.Status = "missing_credential"
			} else if v.Expired {
				v.Status = "token_expired"
			} else if status == nil {
				v.Status = "gateway_unreachable"
			}
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Nickname != out[j].Nickname {
			return out[i].Nickname < out[j].Nickname
		}
		return out[i].UID < out[j].UID
	})
	_ = gwErr // 网关错误通过 status==nil 体现，由 Overview 暴露详情
	return out, status, fileIssues, nil
}

// Overview 汇总仪表盘数据。
func (s *Service) Overview(ctx context.Context) *Overview {
	accounts, status, fileIssues, _ := s.Accounts(ctx)
	ov := &Overview{
		GatewayURL:   s.gw.BaseURL(),
		FileCount:    len(accounts),
		ReadOnly:     s.cfg.ReadOnly,
		DangerousOps: s.cfg.DangerousOps,
		ServerTime:   time.Now(),
		FileIssues:   fileIssues,
	}
	// 网关可达性 + 身份校验。
	if h, err := s.gw.Health(ctx); err == nil {
		ov.GatewayOK = true
		ov.Health = h
	} else {
		ov.GatewayError = err.Error()
	}
	if status != nil {
		ov.Total = status.Total
		ov.Healthy = status.Healthy
		ov.Cooling = status.Cooling
		ov.Disabled = status.Disabled
		ov.InFlightFull = status.InFlightFull
		ov.StickySessions = status.StickySessions
		ov.RedisMode = status.RedisMode
	}
	var inFlight int
	for _, a := range accounts {
		inFlight += a.InFlight
		if a.Expired {
			ov.Expired++
		} else if a.NeedsRefresh {
			ov.Expiring++
		}
		if !a.HasFile && a.InGateway {
			ov.Warnings = append(ov.Warnings,
				fmt.Sprintf("账号 %s 在网关池中但磁盘无凭证文件，重启后将消失", shortUID(a.UID)))
		}
		if a.HasFile && !a.InGateway && status != nil {
			// 区分两种成因，给出可操作的指引：
			//  ① 凭证文件权限让网关读不到（容器里面板 root 写、网关低权限用户读）
			//     —— 这种情况单纯重启网关也没用，必须先修权限。
			//  ② 权限正常，只是网关还没重启扫描到新文件。
			if hint := s.credentialReadabilityHint(a.UID); hint != "" {
				ov.Warnings = append(ov.Warnings, fmt.Sprintf("账号 %s 已落盘但网关无法读取：%s", shortUID(a.UID), hint))
			} else {
				ov.Warnings = append(ov.Warnings,
					fmt.Sprintf("账号 %s 有凭证文件但不在网关池中，需重启网关加载", shortUID(a.UID)))
			}
		}
		// 积分汇总口径：主动查询结果优先，其次用网关 /status 里缓存的积分。
		// 两者都没有（例如凭证文件存在但网关未加载该账号）才算「未取到」，据实计入 failed。
		ov.Credits.Accounts++
		switch {
		case a.LiveCredits != nil:
			ov.Credits.Remain += *a.LiveCredits
			ov.Credits.OK++
		case a.InGateway:
			ov.Credits.Remain += a.GatewayCredits
			ov.Credits.OK++
		default:
			ov.Credits.Failed++
		}
	}
	ov.InFlight = inFlight
	if ov.RedisMode == "" {
		ov.RedisMode = "noop"
	}
	s.mu.RLock()
	totalSize := int64(0)
	used := int64(0)
	for _, c := range s.credits {
		if c.credits != nil {
			totalSize += c.credits.Size
			used += c.credits.Used
		}
	}
	s.mu.RUnlock()
	ov.Credits.Size = totalSize
	ov.Credits.Used = used
	ov.Credits.Failed = ov.Credits.Accounts - ov.Credits.OK
	return ov
}

// ---------------------------------------------------------------------------
// 单账号操作
// ---------------------------------------------------------------------------

// loadAccount 读取账号凭证。
func (s *Service) loadAccount(uid string) (*authstore.Account, error) {
	a, err := s.store.Get(uid)
	if err != nil {
		return nil, describeAccountLoadError(uid, err)
	}
	return a, nil
}

// describeAccountLoadError 把读凭证的底层错误转成面向用户的文案。
//
// 为什么不直接把 err 抛出去：os.ReadFile 的错误里带着服务器上的绝对路径
// （如 /root/workbuddy2api/auths/...），对使用者是噪音，也会泄露部署细节。
func describeAccountLoadError(uid string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("账号不存在：%s（凭证文件已被删除？）", shortUID(uid))
	case os.IsPermission(err):
		return fmt.Errorf("没有读取账号 %s 凭证文件的权限（请检查文件权限）", shortUID(uid))
	default:
		if authstore.ValidUID(uid) == false {
			return fmt.Errorf("非法 uid：%q", uid)
		}
		return fmt.Errorf("读取账号 %s 失败：%v", shortUID(uid), err)
	}
}

// Checkin 对单账号执行签到 + 余额刷新。
func (s *Service) Checkin(ctx context.Context, uid string) (*OpResult, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	a, err := s.loadAccount(uid)
	if err != nil {
		return nil, err
	}
	res := &OpResult{UID: uid, Action: "checkin", OK: true}

	// 先刷新过期 token，否则签到必然 401。
	if a.NeedsRefresh(10 * time.Minute) {
		if err := s.refreshAccount(a); err != nil {
			return &OpResult{UID: uid, Action: "checkin", OK: false,
				Message: "刷新 token 失败: " + err.Error()}, nil
		}
	}
	cr, err := s.up.DailyCheckin(a)
	switch {
	case err != nil:
		res.OK = false
		res.Message = "签到失败: " + err.Error()
	case cr.Already:
		res.Message = "今日已签到"
	default:
		res.Message = cr.Message
	}
	// 无论签到结果如何，都刷新一次余额（可能已恢复额度）。
	if credits, cerr := s.up.UserResource(a); cerr == nil {
		s.setCreditCache(uid, credits)
		res.Data = map[string]any{"credits": credits}
	}
	return res, nil
}

// RefreshToken 刷新单账号 token 并落盘。
func (s *Service) RefreshToken(ctx context.Context, uid string) (*OpResult, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	a, err := s.loadAccount(uid)
	if err != nil {
		return nil, err
	}
	before := a.ExpiresAt
	if err := s.refreshAccount(a); err != nil {
		return &OpResult{UID: uid, Action: "refresh", OK: false, Message: err.Error()}, nil
	}
	return &OpResult{
		UID: uid, Action: "refresh", OK: true,
		Message: fmt.Sprintf("token 已刷新，有效期至 %s", a.ExpiresAtTime().Format("2006-01-02 15:04:05")),
		Data: map[string]any{
			"expires_at":       a.ExpiresAt,
			"expires_before":   before,
			"expires_at_human": a.ExpiresAtTime().Format(time.RFC3339),
		},
	}, nil
}

// refreshAccount 刷新并原子落盘。
func (s *Service) refreshAccount(a *authstore.Account) error {
	if err := s.up.RefreshToken(a); err != nil {
		return err
	}
	if err := s.store.Save(a); err != nil {
		return fmt.Errorf("刷新成功但落盘失败: %w", err)
	}
	return nil
}

// Travel 对单账号推进一趟猫猫旅行。
func (s *Service) Travel(ctx context.Context, uid string) (*OpResult, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	a, err := s.loadAccount(uid)
	if err != nil {
		return nil, err
	}
	if a.NeedsRefresh(10 * time.Minute) {
		if err := s.refreshAccount(a); err != nil {
			return &OpResult{UID: uid, Action: "travel", OK: false,
				Message: "刷新 token 失败: " + err.Error()}, nil
		}
	}
	tr, err := s.up.TravelOnce(a)
	if err != nil {
		msg := tr.Message
		if msg == "" {
			msg = err.Error()
		}
		return &OpResult{UID: uid, Action: "travel", OK: false, Message: msg}, nil
	}
	ok := tr.Action != "error"
	return &OpResult{
		UID: uid, Action: "travel", OK: ok,
		Message: tr.Message, Reward: tr.Reward,
		Data: map[string]any{"travel_action": tr.Action, "buddy": tr.Buddy},
	}, nil
}

// CreditsFor 主动查询单账号积分（结果进缓存）。
func (s *Service) CreditsFor(ctx context.Context, uid string) (*upstream.Credits, error) {
	a, err := s.loadAccount(uid)
	if err != nil {
		return nil, err
	}
	if a.NeedsRefresh(10 * time.Minute) {
		if err := s.refreshAccount(a); err != nil {
			return nil, fmt.Errorf("刷新 token 失败: %w", err)
		}
	}
	c, err := s.up.UserResource(a)
	if err != nil {
		s.mu.Lock()
		s.credits[uid] = creditCache{at: time.Now(), err: err.Error()}
		s.mu.Unlock()
		return nil, err
	}
	s.setCreditCache(uid, c)
	return c, nil
}

// Profile 返回单账号的完整详情（含实时积分与猫档案）。
//
// silent=true 时跳过所有可能失败的上游调用，只返回本地已知信息。
func (s *Service) Profile(ctx context.Context, uid string, silent bool) (map[string]any, error) {
	accounts, _, _, _ := s.Accounts(ctx)
	var view *AccountView
	for i := range accounts {
		if accounts[i].UID == uid {
			view = &accounts[i]
			break
		}
	}
	if view == nil {
		return nil, fmt.Errorf("账号不存在: %s", uid)
	}
	out := map[string]any{"account": view}
	if silent {
		return out, nil
	}
	a, err := s.store.Get(uid)
	if err != nil {
		out["upstream_error"] = "读取凭证失败: " + err.Error()
		return out, nil
	}
	if a.NeedsRefresh(10 * time.Minute) {
		if err := s.refreshAccount(a); err != nil {
			out["upstream_error"] = "刷新 token 失败: " + err.Error()
			return out, nil
		}
	}
	if c, err := s.up.UserResource(a); err == nil {
		s.setCreditCache(uid, c)
		out["credits"] = c
	} else {
		out["credits_error"] = err.Error()
	}
	if b, err := s.up.BuddyInfo(a); err == nil {
		out["buddy"] = b
	} else {
		out["buddy_error"] = err.Error()
	}
	if ts, err := s.up.TravelStatus(a); err == nil {
		out["travel"] = ts
	} else {
		out["travel_error"] = err.Error()
	}
	return out, nil
}

// setCreditCache 写入积分缓存。
func (s *Service) setCreditCache(uid string, c *upstream.Credits) {
	s.mu.Lock()
	s.credits[uid] = creditCache{credits: c, at: time.Now()}
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 批量操作（异步任务）
// ---------------------------------------------------------------------------

// targetAccounts 解析批量操作的目标账号：uids 为空表示全量。
func (s *Service) targetAccounts(ctx context.Context, uids []string) ([]*authstore.Account, error) {
	if len(uids) > 0 {
		out := make([]*authstore.Account, 0, len(uids))
		for _, uid := range uids {
			a, err := s.store.Get(uid)
			if err != nil {
				return nil, fmt.Errorf("账号 %s: %w", shortUID(uid), err)
			}
			out = append(out, a)
		}
		return out, nil
	}
	all, _ := s.store.List()
	return all, nil
}

// BatchCheckin 批量签到 + 余额刷新（异步任务）。
func (s *Service) BatchCheckin(ctx context.Context, uids []string) (*TaskView, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	accounts, err := s.targetAccounts(ctx, uids)
	if err != nil {
		return nil, err
	}
	return s.tasks.New("checkin", fmt.Sprintf("批量签到（%d 个账号）", len(accounts)), func(t *Task) {
		for i, a := range accounts {
			if i > 0 {
				time.Sleep(checkinAccountDelay)
			}
			t.addItem(s.checkinOne(a))
		}
	}), nil
}

// checkinOne 单账号签到（批量任务内部使用）。
func (s *Service) checkinOne(a *authstore.Account) TaskItem {
	item := TaskItem{
		UID: a.UID, Nickname: a.Nickname, Action: "checkin",
		OK: true, StartedAt: time.Now(),
	}
	defer func() { item.EndedAt = time.Now() }()
	if a.NeedsRefresh(10 * time.Minute) {
		if err := s.refreshAccount(a); err != nil {
			item.OK = false
			item.Message = "刷新 token 失败: " + err.Error()
			return item
		}
	}
	cr, err := s.up.DailyCheckin(a)
	switch {
	case err != nil:
		item.OK = false
		item.Message = err.Error()
	case cr.Already:
		item.Message = "今日已签到"
	default:
		item.Message = cr.Message
	}
	if c, cerr := s.up.UserResource(a); cerr == nil {
		s.setCreditCache(a.UID, c)
		item.Message += fmt.Sprintf("｜余额 %d", c.Remain)
	}
	return item
}

// BatchRefresh 批量刷新 token（异步任务）。
func (s *Service) BatchRefresh(ctx context.Context, uids []string) (*TaskView, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	accounts, err := s.targetAccounts(ctx, uids)
	if err != nil {
		return nil, err
	}
	return s.tasks.New("refresh", fmt.Sprintf("批量刷新 Token（%d 个账号）", len(accounts)), func(t *Task) {
		for i, a := range accounts {
			if i > 0 {
				time.Sleep(checkinAccountDelay)
			}
			start := time.Now()
			item := TaskItem{UID: a.UID, Nickname: a.Nickname, Action: "refresh", OK: true, StartedAt: start}
			if err := s.refreshAccount(a); err != nil {
				item.OK = false
				item.Message = err.Error()
			} else {
				item.Message = "有效期至 " + a.ExpiresAtTime().Format("2006-01-02 15:04:05")
			}
			item.EndedAt = time.Now()
			t.addItem(item)
		}
	}), nil
}

// BatchTravel 批量推进猫猫旅行（异步任务）。
func (s *Service) BatchTravel(ctx context.Context, uids []string) (*TaskView, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	accounts, err := s.targetAccounts(ctx, uids)
	if err != nil {
		return nil, err
	}
	return s.tasks.New("travel", fmt.Sprintf("批量猫猫旅行（%d 个账号）", len(accounts)), func(t *Task) {
		for i, a := range accounts {
			if i > 0 {
				time.Sleep(travelAccountDelay)
			}
			start := time.Now()
			item := TaskItem{UID: a.UID, Nickname: a.Nickname, Action: "travel", OK: true, StartedAt: start}
			if a.NeedsRefresh(10 * time.Minute) {
				if err := s.refreshAccount(a); err != nil {
					item.OK = false
					item.Message = "刷新 token 失败: " + err.Error()
					item.EndedAt = time.Now()
					t.addItem(item)
					continue
				}
			}
			tr, err := s.up.TravelOnce(a)
			switch {
			case err != nil:
				item.OK = false
				if tr != nil && tr.Message != "" {
					item.Message = tr.Message
				} else {
					item.Message = err.Error()
				}
			default:
				item.Message = tr.Message
				item.Reward = tr.Reward
			}
			item.EndedAt = time.Now()
			t.addItem(item)
		}
	}), nil
}

// BatchCredits 批量查询积分（异步任务，结果写缓存）。
func (s *Service) BatchCredits(ctx context.Context, uids []string) (*TaskView, error) {
	accounts, err := s.targetAccounts(ctx, uids)
	if err != nil {
		return nil, err
	}
	return s.tasks.New("credits", fmt.Sprintf("批量查询积分（%d 个账号）", len(accounts)), func(t *Task) {
		for i, a := range accounts {
			if i > 0 {
				time.Sleep(checkinAccountDelay)
			}
			start := time.Now()
			item := TaskItem{UID: a.UID, Nickname: a.Nickname, Action: "credits", OK: true, StartedAt: start}
			if a.NeedsRefresh(10 * time.Minute) {
				if err := s.refreshAccount(a); err != nil {
					item.OK = false
					item.Message = "刷新 token 失败: " + err.Error()
					item.EndedAt = time.Now()
					t.addItem(item)
					continue
				}
			}
			c, err := s.up.UserResource(a)
			if err != nil {
				item.OK = false
				item.Message = err.Error()
			} else {
				s.setCreditCache(a.UID, c)
				item.Message = fmt.Sprintf("剩余 %d / 总量 %d（%d 个套餐）", c.Remain, c.Size, c.Packages)
			}
			item.EndedAt = time.Now()
			t.addItem(item)
		}
	}), nil
}

// ---------------------------------------------------------------------------
// 账号增删改
// ---------------------------------------------------------------------------

// DeleteAccount 删除账号凭证（高危：不可从上游恢复）。
func (s *Service) DeleteAccount(ctx context.Context, uid string) error {
	if err := s.ensureDangerous(); err != nil {
		return err
	}
	return s.store.Delete(uid)
}

// StateFileInfo 池状态文件信息（只读展示）。
type StateFileInfo struct {
	Path     string    `json:"path"`
	Exists   bool      `json:"exists"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time,omitempty"`
	Accounts int       `json:"accounts"`
	Err      string    `json:"error,omitempty"`
}

// credentialReadabilityHint 检查凭证文件是否「对其他用户不可读」。
//
// 场景：面板以 root 运行（写宿主机挂载的凭证目录），网关容器以低权限用户
// （官方镜像里是 uid 10001 的 app）读取同一目录。若凭证是 root:600，网关
// open() 会 permission denied，账号永远加载不进池 —— 此时只提示「重启网关」
// 会误导用户（重启也没用）。这里检出该情况并给出具体修法。
//
// 返回空串表示权限没问题（那么「未加载」的原因就只剩「网关还没重启」）。
func (s *Service) credentialReadabilityHint(uid string) string {
	p := s.store.PathFor(uid)
	if p == "" {
		return ""
	}
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	mode := st.Mode().Perm()
	// 组/其他用户可读 → 网关（不同用户）也能读，无权限问题。
	if mode&0o044 != 0 {
		return ""
	}
	uid2, gid := s.store.Owner()
	if fi, ok := st.Sys().(*syscall.Stat_t); ok {
		ownerLine := fmt.Sprintf("当前属主 %d:%d 权限 %o", fi.Uid, fi.Gid, mode)
		fix := fmt.Sprintf("执行 chown %d:%d %s（或设置配置项 auth_owner_uid/auth_owner_gid）",
			fi.Uid, fi.Gid, "auths/workbuddy-"+shortUID(uid)+".json")
		if uid2 >= 0 || gid >= 0 {
			fix = fmt.Sprintf("面板已配置 auth_owner_uid=%d，但本次写入未生效，请检查挂载目录权限", uid2)
		}
		return ownerLine + "，网关以其他用户运行故读不到；" + fix
	}
	return fmt.Sprintf("文件权限 %o 可能过于严格，网关进程读不到", mode)
}

func shortUID(uid string) string {
	if len(uid) > 8 {
		return uid[:8]
	}
	return uid
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
