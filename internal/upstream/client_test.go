package upstream

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"workbuddy2api-gui/internal/authstore"
)

// TestClassify 错误分类决定给用户的处置建议，必须准确。
func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   ErrKind
	}{
		{"余额不足-402", 402, `{"msg":"insufficient credit"}`, ErrHardCredit},
		{"余额不足-中文", 400, `{"msg":"积分不足，请充值"}`, ErrHardCredit},
		{"额度耗尽", 200, `{"msg":"quota exceeded"}`, ErrHardCredit},
		{"会话失效-12153", 401, `{"code":12153,"msg":"12153:refresh token failed"}`, ErrSessionDead},
		{"会话失效-英文", 401, `{"msg":"Offline user session not found"}`, ErrSessionDead},
		{"鉴权失败-无body", 401, ``, ErrSessionDead},
		{"限流-429", 429, `{"msg":"too many requests"}`, ErrSoftRate},
		{"限流-文案", 200, `{"msg":"The model provider is rate-limiting requests."}`, ErrSoftRate},
		{"限流-中文", 400, `{"msg":"请求过于频繁"}`, ErrSoftRate},
		{"上游404", 404, `not found`, ErrNotFound},
		{"上游5xx", 503, `bad gateway`, ErrServer},
		{"客户端400", 400, `{"msg":"bad request"}`, ErrClient},
		{"无错误", 200, `{}`, ErrNone},
	}
	for _, c := range cases {
		if got := Classify(c.status, c.body); got != c.want {
			t.Errorf("%s: Classify(%d, %q) = %v, want %v", c.name, c.status, c.body, got, c.want)
		}
	}
}

// TestClassifyPriority 分类优先级：余额 > 会话失效 > 限流（判定顺序影响处置动作）。
func TestClassifyPriority(t *testing.T) {
	// 同时含余额与限流关键词 → 应判余额不足（最不可自愈，优先人工介入）。
	if got := Classify(402, "rate limit and insufficient quota"); got != ErrHardCredit {
		t.Errorf("余额关键词应优先: %v", got)
	}
	// 同时含 session 与 rate limit → 会话失效（精确 marker 优先于宽泛子串）。
	if got := Classify(401, "Offline user session not found, please rate limit"); got != ErrSessionDead {
		t.Errorf("会话失效 marker 应优先: %v", got)
	}
}

// TestErrKindString 每类错误都要有中文说明（直接展示给用户）。
func TestErrKindString(t *testing.T) {
	kinds := []ErrKind{ErrNone, ErrHardCredit, ErrSoftRate, ErrSessionDead, ErrNotFound, ErrServer, ErrClient}
	for _, k := range kinds {
		if s := k.String(); s == "" {
			t.Errorf("ErrKind(%d).String() 为空", k)
		}
	}
	if !strings.Contains(ErrSessionDead.String(), "重新登录") {
		t.Errorf("会话失效说明应含处置建议: %q", ErrSessionDead.String())
	}
	if !strings.Contains(ErrHardCredit.String(), "余额") {
		t.Errorf("余额不足说明不正确: %q", ErrHardCredit.String())
	}
}

// TestErrorFormatting 错误文案应含状态码与分类，便于排查。
func TestErrorFormatting(t *testing.T) {
	e := &Error{Kind: ErrSoftRate, Status: 429, Msg: "too many"}
	got := e.Error()
	if !strings.Contains(got, "429") || !strings.Contains(got, "被限流") {
		t.Errorf("错误文案不完整: %q", got)
	}
	// 无状态码时不应出现 "HTTP 0"。
	e2 := &Error{Kind: ErrClient, Msg: "x"}
	if strings.Contains(e2.Error(), "HTTP") {
		t.Errorf("无状态码时不应带 HTTP: %q", e2.Error())
	}
}

// TestIsSessionDead 判定辅助函数。
func TestIsSessionDead(t *testing.T) {
	if !IsSessionDead(&Error{Kind: ErrSessionDead}) {
		t.Error("ErrSessionDead 应被识别")
	}
	if IsSessionDead(&Error{Kind: ErrSoftRate}) {
		t.Error("ErrSoftRate 不应被识别为会话失效")
	}
	if IsSessionDead(errors.New("plain error")) {
		t.Error("普通错误不应被识别")
	}
	// 包装后仍应识别（错误链路里常用 %w 包装）。
	wrapped := fmt.Errorf("refresh failed: %w", &Error{Kind: ErrSessionDead, Status: 401})
	if !IsSessionDead(wrapped) {
		t.Error("包装后的 ErrSessionDead 应被识别")
	}
}

// TestIsAlreadyCheckedIn 已签到是幂等成功，不能当失败处理。
func TestIsAlreadyCheckedIn(t *testing.T) {
	already := []string{
		"code=10001 msg=今天已签到",
		"already checked in",
		"daily checkin done",
		"用户已签到",
	}
	for _, s := range already {
		if !IsAlreadyCheckedIn(errors.New(s)) {
			t.Errorf("应识别为已签到: %q", s)
		}
	}
	if IsAlreadyCheckedIn(errors.New("network timeout")) {
		t.Error("网络错误不应被识别为已签到")
	}
	if IsAlreadyCheckedIn(nil) {
		t.Error("nil 不应被识别")
	}
}

// TestIsBuddyTaskIncomplete 领养门槛未达标（HTTP 400 + 关键词）。
func TestIsBuddyTaskIncomplete(t *testing.T) {
	e := &Error{Kind: ErrClient, Status: http.StatusBadRequest, Msg: "first_buddy task not completed yet"}
	if !IsBuddyTaskIncomplete(e) {
		t.Error("应识别为领养门槛未达标")
	}
	// 同样的消息但状态码不是 400 → 不算。
	if IsBuddyTaskIncomplete(&Error{Status: http.StatusInternalServerError, Msg: "first_buddy task not completed yet"}) {
		t.Error("非 400 不应识别")
	}
	if IsBuddyTaskIncomplete(errors.New("other")) {
		t.Error("其他错误不应识别")
	}
}

// TestDescribeBody HTML 错误页应转成人话，不能把一整坨标签丢给用户。
func TestDescribeBody(t *testing.T) {
	html401 := []byte(`<html><head><title>401 Authorization Required</title></head><body>...`)
	got := describeBody(401, html401)
	if strings.Contains(got, "<html") {
		t.Errorf("不应把 HTML 原样透出: %q", got)
	}
	if !strings.Contains(got, "token") {
		t.Errorf("401 应给出 token 相关建议: %q", got)
	}
	// 重复状态码：Error.Error() 已带 HTTP 码，body 文案里不该再带一遍。
	if strings.Contains(got, "HTTP 401") {
		t.Errorf("状态码不应重复出现: %q", got)
	}

	if got := describeBody(404, []byte("<html>nope</html>")); !strings.Contains(got, "不存在") {
		t.Errorf("404 HTML 文案: %q", got)
	}
	// 空响应。
	if got := describeBody(500, nil); !strings.Contains(got, "空响应") {
		t.Errorf("空响应文案: %q", got)
	}
	// 正常 JSON 文本应保留（截断到 200 字符）。
	long := strings.Repeat("x", 300)
	if got := describeBody(400, []byte(long)); len(got) != 200 {
		t.Errorf("长文本应截断到 200, 得到 %d", len(got))
	}
}

// TestTruncate 截断辅助函数（含换行折叠）。
func TestTruncate(t *testing.T) {
	if got := truncate("  a\nb  ", 10); got != "a b" {
		t.Errorf("truncate = %q, want %q", got, "a b")
	}
	if got := truncate("abcdef", 3); got != "abc" {
		t.Errorf("truncate = %q, want abc", got)
	}
}

// TestStartLoginRejectsMissingFields 上游响应缺字段时应报错而非返回空值。
func TestStartLoginRejectsMissingFields(t *testing.T) {
	// 端点地址是常量，无法在单测里替换；这里验证入参校验分支。
	c := New(5)
	if _, err := c.PollLogin(RegionCN, ""); err == nil {
		t.Error("空 state 应报错")
	}
	if _, err := c.PollLogin(RegionCN, "   "); err == nil {
		t.Error("空白 state 应报错")
	}
}

// TestNormalizeRegion 区域规范化。
func TestNormalizeRegion(t *testing.T) {
	cases := map[string]Region{
		"":       RegionCN,
		"cn":     RegionCN,
		"CN":     RegionCN,
		"  Cn ":  RegionCN,
		"global": RegionGlobal,
		"GLOBAL": RegionGlobal,
	}
	for in, want := range cases {
		if got, err := NormalizeRegion(in); err != nil || got != want {
			t.Errorf("NormalizeRegion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := NormalizeRegion("japan"); err == nil {
		t.Error("非法区域应报错")
	}
}

// TestRegionOfAccount 按账号 domain 反推区域。
func TestRegionOfAccount(t *testing.T) {
	cases := []struct {
		domain string
		want   Region
	}{
		{"www.workbuddy.ai", RegionGlobal},
		{"workbuddy.ai", RegionGlobal},
		{"copilot.tencent.com", RegionCN},
		{"codebuddy.cn", RegionCN},
		{"", RegionCN},
	}
	for _, c := range cases {
		if got := regionOfAccount(&authstore.Account{Domain: c.domain}); got != c.want {
			t.Errorf("regionOfAccount(%q) = %v, want %v", c.domain, got, c.want)
		}
	}
}

// TestRegionBases 区域基址与 Origin。
func TestRegionBases(t *testing.T) {
	chat, billing, origin := regionBases(RegionGlobal)
	if chat != "https://www.workbuddy.ai" || billing != "https://www.workbuddy.ai" || origin != "https://www.workbuddy.ai" {
		t.Errorf("global bases = %q/%q/%q", chat, billing, origin)
	}
	chat, billing, origin = regionBases(RegionCN)
	if chat != "https://copilot.tencent.com" || billing != "https://www.codebuddy.cn" || origin != "https://www.codebuddy.cn" {
		t.Errorf("cn bases = %q/%q/%q", chat, billing, origin)
	}
}
