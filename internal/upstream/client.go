// Package upstream 直连腾讯 CodeBuddy 上游（copilot.tencent.com / codebuddy.cn）的客户端，
// 用于 GUI 侧的 OAuth 登录、token 刷新、签到、余额查询与猫猫旅行。
//
// 请求头/路径/信封格式与 workbuddy2api/internal/upstream 保持一致，保证同一账号
// 在网关与 GUI 两侧看到的指纹相同、行为可预期。
package upstream

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"workbuddy2api-gui/internal/authstore"
)

// 上游常量（CN realm）。
const (
	ChatBaseCN    = "https://copilot.tencent.com"
	BillingBaseCN = "https://www.codebuddy.cn"

	clientUA      = "CLI/2.63.2 CodeBuddy/2.63.2"
	originReferer = "https://www.codebuddy.cn"

	// EndpointAuthState 设备授权：申请 state + authUrl。
	EndpointAuthState = ChatBaseCN + "/v2/plugin/auth/state?platform=CLI"
	// EndpointAuthToken 轮询登录结果（state 由服务端签发，无 PKCE）。
	EndpointAuthToken = ChatBaseCN + "/v2/plugin/auth/token?state="
	// EndpointLoginAccount 拿 uid / nickname / enterpriseId。
	EndpointLoginAccount = ChatBaseCN + "/v2/plugin/login/account?state="

	// growth 域「猫猫旅行」路径。
	TravelStatusPath   = "/activity/growth/buddy/travel/status"
	TravelDepartPath   = "/activity/growth/buddy/travel/depart"
	TravelClaimPath    = "/activity/growth/buddy/travel/claim"
	BuddyInfoPath      = "/activity/growth/buddy/info"
	BuddyFirstPath     = "/activity/growth/buddy/first"
	BuddyAgreementPath = "/activity/growth/buddy/agreement"

	// TravelLocationID 派出地点固定 4（古镇客栈）：4 个地点收益/时长区间完全相同，无最优解。
	TravelLocationID = 4
)

// buddyTaskIncompleteMarker 领养门槛未达标的业务错误关键词。
const buddyTaskIncompleteMarker = "first_buddy task not completed yet"

// ErrKind 错误分类，用于给前端区分「账号失效」「限流」「余额不足」等处置建议。
type ErrKind int

const (
	// ErrNone 未分类。
	ErrNone ErrKind = iota
	// ErrHardCredit 余额/额度耗尽（HTTP 402 或余额关键词）。
	ErrHardCredit
	// ErrSoftRate 限流（HTTP 429 或限流文案）。
	ErrSoftRate
	// ErrSessionDead 会话失效，需人工重新登录。
	ErrSessionDead
	// ErrNotFound 上游路径 404。
	ErrNotFound
	// ErrServer 上游 5xx。
	ErrServer
	// ErrClient 其余 4xx / 业务 code != 0。
	ErrClient
)

func (k ErrKind) String() string {
	switch k {
	case ErrHardCredit:
		return "余额不足"
	case ErrSoftRate:
		return "被限流"
	case ErrSessionDead:
		return "会话失效（需重新登录）"
	case ErrNotFound:
		return "上游路径不存在"
	case ErrServer:
		return "上游服务端错误"
	case ErrClient:
		return "客户端/业务错误"
	}
	return "未知"
}

// Error 带分类的上游错误。
type Error struct {
	Kind   ErrKind
	Status int
	Msg    string
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s (HTTP %d): %s", e.Kind, e.Status, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

// hardMarkers 余额不足关键词（小写匹配）。
var hardMarkers = []string{
	"insufficient", "quota exceeded", "no enough", "not enough credit",
	"balance", "余额不足", "积分不足", "额度不足", "欠费",
}

// softRateMarkers 限流关键词（小写匹配）。
var softRateMarkers = []string{
	"rate limit", "rate-limit", "rate_limit", "too many requests", "too many request",
	"请求过于频繁", "限流", "频率", "slow down", "throttl",
}

// sessionDeadMarkers 会话失效关键词。
var sessionDeadMarkers = []string{
	"offline user session not found", "12153", "session not found", "session expired", "登录已失效",
}

// Classify 按 HTTP 状态码 + body 判定错误类别（判定顺序自严到宽）。
func Classify(status int, body string) ErrKind {
	lower := strings.ToLower(body)
	has := func(markers []string) bool {
		for _, m := range markers {
			if strings.Contains(lower, m) {
				return true
			}
		}
		return false
	}
	// 1. 计费耗尽最严，最先判。
	if status == http.StatusPaymentRequired || has(hardMarkers) {
		return ErrHardCredit
	}
	// 2. 会话失效：需要人工重登的终态，比限流更具体。
	if has(sessionDeadMarkers) {
		return ErrSessionDead
	}
	// 3. 非 429 状态码携带限流文案。
	if has(softRateMarkers) {
		return ErrSoftRate
	}
	switch {
	case status == http.StatusTooManyRequests:
		return ErrSoftRate
	case status == http.StatusUnauthorized:
		// 鉴权层直接拒绝（token 失效/过期）：需要刷新或重新登录，按会话失效归类最可操作。
		return ErrSessionDead
	case status == http.StatusNotFound:
		return ErrNotFound
	case status >= 500:
		return ErrServer
	case status >= 400:
		return ErrClient
	}
	return ErrNone
}

// IsSessionDead 报告错误是否为「会话失效」。
func IsSessionDead(err error) bool {
	var ue *Error
	return errors.As(err, &ue) && ue.Kind == ErrSessionDead
}

// IsAlreadyCheckedIn 判定签到接口返回的是「今日已签到」这类幂等成功。
func IsAlreadyCheckedIn(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, m := range []string{"已签到", "already", "checkin", "code=10001", "签到过"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// IsBuddyTaskIncomplete 判定领养门槛未达标（HTTP 400 + first_buddy 关键词）。
func IsBuddyTaskIncomplete(err error) bool {
	var ue *Error
	if !errors.As(err, &ue) || ue.Status != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(ue.Msg), buddyTaskIncompleteMarker)
}

// Client 上游 HTTP 客户端。
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// New 构建客户端；timeout <=0 时用 120s。
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		HTTP: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		Timeout: timeout,
	}
}

// apiEnvelope 上游统一信封。
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// commonHeaders 设置所有 API 共享的请求头。
func commonHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", originReferer)
	req.Header.Set("Referer", originReferer+"/")
	req.Header.Set("User-Agent", clientUA)
}

// billingHeaders billing / growth 域请求头。
func billingHeaders(req *http.Request, a *authstore.Account) {
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if a.UID != "" {
		req.Header.Set("X-User-Id", a.UID)
	}
	if a.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", a.EnterpriseID)
		req.Header.Set("X-Tenant-Id", a.EnterpriseID)
	}
	if a.Domain != "" {
		req.Header.Set("X-Domain", a.Domain)
	}
}

// doJSON 发请求并解信封；HTTP 非 2xx 或业务 code != 0 时返回带分类的 *Error。
func (c *Client) doJSON(req *http.Request) (json.RawMessage, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, &Error{
			Kind:   Classify(resp.StatusCode, string(raw)),
			Status: resp.StatusCode,
			Msg:    describeBody(resp.StatusCode, raw),
		}
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w (body: %s)", err, truncate(string(raw), 120))
	}
	if env.Code != 0 {
		kind := Classify(resp.StatusCode, env.Msg)
		if kind == ErrNone {
			kind = ErrClient
		}
		return nil, &Error{
			Kind:   kind,
			Status: resp.StatusCode,
			Msg:    fmt.Sprintf("code=%d msg=%s", env.Code, truncate(env.Msg, 160)),
		}
	}
	return env.Data, nil
}

// describeBody 把上游错误响应体转成可读文案（不含状态码——状态码由 Error.Error() 统一带出）。
//
// 上游在网关层（openresty/apisix）拒绝请求时返回的是 HTML 错误页，而不是 JSON 信封。
// 直接把 HTML 塞进错误信息会让用户看到一大坨标签却看不出问题；这里识别 HTML 后
// 换成人话，并保留可操作的处置建议（401 通常意味着 token 失效）。
func describeBody(status int, raw []byte) string {
	body := strings.TrimSpace(string(raw))
	looksHTML := strings.HasPrefix(body, "<") || strings.Contains(strings.ToLower(body), "<html")
	if looksHTML {
		switch status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "上游拒绝授权（token 可能已失效，请刷新或重新登录）"
		case http.StatusNotFound:
			return "上游接口不存在（域名或路径可能已变更）"
		default:
			return "上游返回了非 JSON 的错误页"
		}
	}
	if body == "" {
		return "上游返回空响应"
	}
	return truncate(body, 200)
}

// ---------------------------------------------------------------------------
// OAuth 设备授权登录
// ---------------------------------------------------------------------------

// StartLogin 申请设备授权，返回 state 与用户需在浏览器打开的授权 URL。
func (c *Client) StartLogin() (state, authURL string, err error) {
	req, err := http.NewRequest(http.MethodPost, EndpointAuthState, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", "", err
	}
	commonHeaders(req)
	data, err := c.doJSON(req)
	if err != nil {
		return "", "", err
	}
	var st struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return "", "", fmt.Errorf("授权响应解析失败: %w", err)
	}
	if st.State == "" || st.AuthURL == "" {
		return "", "", fmt.Errorf("授权响应缺少 state 或 authUrl")
	}
	return st.State, st.AuthURL, nil
}

// PollLogin 轮询一次登录结果。
//
// 返回 (nil, nil) 表示用户尚未完成登录（预期状态，前端继续轮询）；
// 返回非 nil Account 表示登录成功（accessToken/uid 已填充，但尚未落盘）。
func (c *Client) PollLogin(state string) (*authstore.Account, error) {
	if strings.TrimSpace(state) == "" {
		return nil, fmt.Errorf("缺少 state")
	}
	req, err := http.NewRequest(http.MethodGet, EndpointAuthToken+state, nil)
	if err != nil {
		return nil, err
	}
	commonHeaders(req)
	data, err := c.doJSON(req)
	if err != nil {
		var ue *Error
		// 服务端「登录未完成」以业务 code != 0 表达；5xx/网络错误才是真失败。
		if errors.As(err, &ue) && ue.Status > 0 && ue.Status < 500 {
			return nil, nil
		}
		return nil, err
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		// 拿不到 token 视为尚未完成，让前端继续轮询。
		return nil, nil
	}
	acct := &authstore.Account{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Domain:       tok.Domain,
	}
	if tok.ExpiresIn > 0 {
		acct.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}
	// login/account 拿 uid / nickname / enterpriseId（带 Bearer；失败不视为登录失败）。
	acctReq, err := http.NewRequest(http.MethodGet, EndpointLoginAccount+state, nil)
	if err == nil {
		commonHeaders(acctReq)
		acctReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		if resp, err := c.HTTP.Do(acctReq); err == nil {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			var env apiEnvelope
			if json.Unmarshal(raw, &env) == nil && env.Code == 0 {
				var info struct {
					UID          string `json:"uid"`
					EnterpriseID string `json:"enterpriseId"`
					Nickname     string `json:"nickname"`
				}
				if json.Unmarshal(env.Data, &info) == nil {
					acct.UID = info.UID
					acct.EnterpriseID = info.EnterpriseID
					acct.Nickname = info.Nickname
				}
			}
		}
	}
	if acct.UID == "" {
		return nil, fmt.Errorf("登录成功但未能获取 uid，请稍后重试或改用 login.sh")
	}
	if acct.Domain == "" {
		acct.Domain = "copilot.tencent.com"
	}
	return acct, nil
}

// ---------------------------------------------------------------------------
// Token / 签到 / 余额
// ---------------------------------------------------------------------------

// RefreshToken 刷新 access token，就地更新 a（不落盘，调用方负责 Save）。
func (c *Client) RefreshToken(a *authstore.Account) error {
	if strings.TrimSpace(a.RefreshToken) == "" {
		return fmt.Errorf("账号缺少 refreshToken，无法刷新（需重新登录）")
	}
	req, err := http.NewRequest(http.MethodPost, ChatBaseCN+"/v2/plugin/auth/token/refresh", nil)
	if err != nil {
		return err
	}
	commonHeaders(req)
	req.Header.Set("X-Refresh-Token", a.RefreshToken)
	if a.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", a.EnterpriseID)
	}
	req.Header.Set("X-Auth-Refresh-Source", "workbuddy")

	data, err := c.doJSON(req)
	if err != nil {
		return err
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return fmt.Errorf("刷新失败：响应无 accessToken，需重新登录")
	}
	a.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		a.RefreshToken = tok.RefreshToken
	}
	if tok.Domain != "" {
		a.Domain = tok.Domain
	}
	// 响应缺 expiresIn 时保留旧过期时间，避免刷新风暴。
	if tok.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}
	return nil
}

// CheckinResult 签到结果。
type CheckinResult struct {
	Already bool
	Message string
	Raw     string
}

// DailyCheckin 执行每日签到。已签到不视为失败，返回 Already=true。
func (c *Client) DailyCheckin(a *authstore.Account) (*CheckinResult, error) {
	req, err := http.NewRequest(http.MethodPost, BillingBaseCN+"/v2/billing/meter/daily-checkin", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	billingHeaders(req, a)
	data, err := c.doJSON(req)
	if err != nil {
		if IsAlreadyCheckedIn(err) {
			return &CheckinResult{Already: true, Message: err.Error()}, nil
		}
		return nil, err
	}
	raw := strings.TrimSpace(string(data))
	msg := "签到成功"
	if raw != "" && raw != "null" {
		msg = "签到成功：" + truncate(raw, 160)
	}
	return &CheckinResult{Message: msg, Raw: raw}, nil
}

// resourcePackage 单个套餐的容量字段。
type resourcePackage struct {
	PackageName         string `json:"PackageName"`
	CapacityRemain      int64  `json:"CapacityRemain"`
	CapacityUsed        int64  `json:"CapacityUsed"`
	CapacitySize        int64  `json:"CapacitySize"`
	CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
	CycleCapacityUsed   int64  `json:"CycleCapacityUsed"`
	CycleCapacitySize   int64  `json:"CycleCapacitySize"`
}

// Credits 账号积分概览。
type Credits struct {
	Remain   int64 `json:"remain"`
	Used     int64 `json:"used"`
	Size     int64 `json:"size"`
	Packages int   `json:"packages"`
}

// UserResource 查询账号可花费积分余额（所有套餐聚合，负值钳 0）。
func (c *Client) UserResource(a *authstore.Account) (*Credits, error) {
	now := time.Now()
	body, _ := json.Marshal(map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
	})
	req, err := http.NewRequest(http.MethodPost, BillingBaseCN+"/v2/billing/meter/get-user-resource", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	billingHeaders(req, a)
	data, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Response struct {
			Data struct {
				TotalDosage int64             `json:"TotalDosage"`
				Accounts    []resourcePackage `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("积分响应解析失败: %w", err)
	}
	out := &Credits{Packages: len(resp.Response.Data.Accounts)}
	for _, p := range resp.Response.Data.Accounts {
		remain, used, size := packageRemainUsed(p)
		out.Remain += remain
		out.Used += used
		out.Size += size
	}
	if out.Size > 0 {
		if derived := out.Size - out.Remain; derived > out.Used {
			out.Used = derived
		}
	}
	if dosage := resp.Response.Data.TotalDosage; dosage > out.Size {
		out.Size = dosage
		if derived := out.Size - out.Remain; derived > out.Used {
			out.Used = derived
		}
	}
	if out.Remain < 0 {
		out.Remain = 0
	}
	return out, nil
}

// packageRemainUsed 按 Cycle* 优先的口径拆出 remain/used/size
// （与 workbuddy2api/cmd/credit 及网关 UserResource 的聚合口径保持一致）。
func packageRemainUsed(p resourcePackage) (remain, used, size int64) {
	if p.CycleCapacitySize > 0 {
		remain = p.CycleCapacityRemain
		size = p.CycleCapacitySize
		if remain < 0 {
			remain = 0
		}
		if remain > size {
			remain = size
		}
		used = size - remain
		if p.CycleCapacityUsed > used {
			used = p.CycleCapacityUsed
			if size >= used {
				remain = size - used
			}
		}
		return remain, used, size
	}
	remain = p.CapacityRemain
	used = p.CapacityUsed
	size = p.CapacitySize
	if used == 0 && size > remain {
		used = size - remain
	}
	return remain, used, size
}

// ---------------------------------------------------------------------------
// 猫猫旅行
// ---------------------------------------------------------------------------

// Buddy 账号当前猫档案；nil 表示无猫。
type Buddy struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// TravelState 猫猫旅行状态。
type TravelState struct {
	State             string `json:"state"`               // idle / traveling / arrived
	DailyLimitReached bool   `json:"daily_limit_reached"` // 今日已派出过（CST 自然日重置）
	RecordID          int64  `json:"record_id"`
	RewardCredit      int64  `json:"reward_credit"`
}

// growthJSON 发 growth 域请求并解信封。
func (c *Client) growthJSON(a *authstore.Account, method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ChatBaseCN+path, rdr)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, a)
	return c.doJSON(req)
}

// TravelStatus 查询猫猫旅行状态。
func (c *Client) TravelStatus(a *authstore.Account) (*TravelState, error) {
	data, err := c.growthJSON(a, http.MethodGet, TravelStatusPath, nil)
	if err != nil {
		return nil, err
	}
	var st TravelState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("旅行状态解析失败: %w", err)
	}
	return &st, nil
}

// TravelDepart 派出猫旅行。
func (c *Client) TravelDepart(a *authstore.Account, locationID int) error {
	_, err := c.growthJSON(a, http.MethodPost, TravelDepartPath, map[string]any{"location_id": locationID})
	return err
}

// TravelClaim 领取到站奖励，返回 reward_credit。
func (c *Client) TravelClaim(a *authstore.Account, recordID int64) (int64, error) {
	data, err := c.growthJSON(a, http.MethodPost, TravelClaimPath, map[string]any{"record_id": recordID})
	if err != nil {
		return 0, err
	}
	var resp struct {
		RewardCredit int64 `json:"reward_credit"`
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &resp)
	}
	return resp.RewardCredit, nil
}

// BuddyInfo 查询当前猫档案；返回 (nil, nil) 表示无猫。
func (c *Client) BuddyInfo(a *authstore.Account) (*Buddy, error) {
	data, err := c.growthJSON(a, http.MethodGet, BuddyInfoPath, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Buddy json.RawMessage `json:"buddy"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(resp.Buddy))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var b Buddy
	if err := json.Unmarshal(resp.Buddy, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// BuddyFirst 领养第一只猫（门槛未达标返回 HTTP 400，属预期）。
func (c *Client) BuddyFirst(a *authstore.Account) error {
	_, err := c.growthJSON(a, http.MethodPost, BuddyFirstPath, map[string]any{})
	return err
}

// BuddyAgreement 同意协议（幂等）。
func (c *Client) BuddyAgreement(a *authstore.Account) error {
	_, err := c.growthJSON(a, http.MethodPost, BuddyAgreementPath, map[string]any{"agree": true})
	return err
}

// TravelResult 单账号一次旅行巡检结果。
type TravelResult struct {
	UID     string `json:"uid"`
	Action  string `json:"action"` // adopt / depart / claim / skip / error
	Message string `json:"message"`
	Reward  int64  `json:"reward,omitempty"`
	Buddy   string `json:"buddy,omitempty"`
}

// TravelOnce 对单个账号推进一趟旅行状态机（单趟只做一个动作，不轮询不等待）。
//
// 状态机与 workbuddy2api/internal/scheduler/travel.go 对齐：
//
//	无猫            → 同意协议 + 尝试领养（门槛未达标属预期，静默跳过）
//	state=idle      → 今日未派出则派出（location_id=4）
//	state=arrived   → 领取到站奖励
//	state=traveling / 今日已达上限 → 跳过
func (c *Client) TravelOnce(a *authstore.Account) (*TravelResult, error) {
	res := &TravelResult{UID: a.UID}
	buddy, err := c.BuddyInfo(a)
	if err != nil {
		res.Action = "error"
		res.Message = "查询猫档案失败: " + err.Error()
		return res, err
	}
	if buddy == nil {
		// 先同意协议（幂等），再尝试领养。
		if err := c.BuddyAgreement(a); err != nil {
			res.Action = "error"
			res.Message = "同意协议失败: " + err.Error()
			return res, err
		}
		if err := c.BuddyFirst(a); err != nil {
			if IsBuddyTaskIncomplete(err) {
				res.Action = "skip"
				res.Message = "领养门槛未达标（今日不再重试）"
				return res, nil
			}
			res.Action = "error"
			res.Message = "领养失败: " + err.Error()
			return res, err
		}
		res.Action = "adopt"
		res.Message = "领养成功（+300 积分）"
		return res, nil
	}
	res.Buddy = buddy.Name

	st, err := c.TravelStatus(a)
	if err != nil {
		res.Action = "error"
		res.Message = "查询旅行状态失败: " + err.Error()
		return res, err
	}
	switch st.State {
	case "idle":
		if st.DailyLimitReached {
			res.Action = "skip"
			res.Message = "今日已派出，等待归来"
			return res, nil
		}
		if err := c.TravelDepart(a, TravelLocationID); err != nil {
			if IsBuddyTaskIncomplete(err) {
				res.Action = "skip"
				res.Message = "派出条件未满足，今日跳过"
				return res, nil
			}
			res.Action = "error"
			res.Message = "派出失败: " + err.Error()
			return res, err
		}
		res.Action = "depart"
		res.Message = "已派猫出门（古镇客栈）"
		return res, nil
	case "arrived":
		reward, err := c.TravelClaim(a, st.RecordID)
		if err != nil {
			res.Action = "error"
			res.Message = "领奖失败: " + err.Error()
			return res, err
		}
		res.Action = "claim"
		res.Reward = reward
		res.Message = fmt.Sprintf("已领取到站奖励 +%d", reward)
		return res, nil
	case "traveling":
		res.Action = "skip"
		res.Message = "猫正在旅行中"
		return res, nil
	default:
		res.Action = "skip"
		res.Message = fmt.Sprintf("未知状态 %q，跳过", st.State)
		return res, nil
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n]
	}
	return s
}
