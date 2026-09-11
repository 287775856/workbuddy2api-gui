// Package gateway 访问 workbuddy2api 网关自身的 OpenAI 兼容 / 管理接口。
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ServicesStatusList 是网关 /status 返回的账号条目（字段与 pool.Status 对齐）。
type AccountStatus struct {
	UID             string    `json:"uid"`
	Nickname        string    `json:"nickname,omitempty"`
	Credits         int64     `json:"credits"`
	Cooling         bool      `json:"cooling"`
	CoolKind        string    `json:"cool_kind,omitempty"`
	CoolRemaining   int64     `json:"cool_remaining_sec,omitempty"`
	Until           time.Time `json:"until,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	SoftStreak      int       `json:"soft_streak,omitempty"`
	Disabled        bool      `json:"disabled"`
	SuccessCount    int64     `json:"success_count,omitempty"`
	ErrTotal        int64     `json:"err_total,omitempty"`
	LastSuccessTime time.Time `json:"last_success,omitempty"`
	LastErrTime     time.Time `json:"last_err,omitempty"`
	InFlight        int       `json:"in_flight"`
	BreakerFails    int       `json:"breaker_fails"`
	BreakerUntil    time.Time `json:"breaker_until,omitempty"`
}

// Status 网关 /status 响应。
type Status struct {
	Accounts       []AccountStatus `json:"accounts"`
	Total          int             `json:"total"`
	Healthy        int             `json:"healthy"`
	Cooling        int             `json:"cooling"`
	Disabled       int             `json:"disabled"`
	InFlightFull   int             `json:"in_flight_full"`
	StickySessions int             `json:"sticky_sessions"`
	RedisMode      string          `json:"redis_mode"`
}

// Health 网关 /healthz 响应。
type Health struct {
	Healthy int    `json:"healthy"`
	Total   int    `json:"total"`
	Service string `json:"service"`
}

// Model OpenAI 模型条目。
type Model struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	ContextLength int64  `json:"context_length"`
	MaxOutput     int64  `json:"max_output_tokens"`
}

// Client 网关客户端。每次请求都携带当前 api_key，因此支持运行期改配置。
type Client struct {
	baseURL string
	apiKey  func() string
	http    *http.Client
}

// New 构建客户端；apiKey 用函数注入以便配置热更新。
func New(baseURL string, apiKey func() string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// BaseURL 返回当前网关地址。
func (c *Client) BaseURL() string { return c.baseURL }

// SetBaseURL 更新网关地址（配置热更新用）。
func (c *Client) SetBaseURL(u string) { c.baseURL = strings.TrimRight(u, "/") }

// do 发一次带鉴权的 GET 请求。
func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if key := c.apiKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// Health 探活网关。注意 /healthz 无鉴权，但需校验 service 字段以确认
// 「打到的确实是 workbuddy2api」，避免同端口其他服务造成的假成功。
func (c *Client) Health(ctx context.Context) (*Health, error) {
	resp, err := c.do(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var h Health
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("网关 /healthz 响应无法解析（可能不是 workbuddy2api）: %w", err)
	}
	if h.Service != "workbuddy2api" {
		return nil, fmt.Errorf("端口上的服务不是 workbuddy2api（service=%q）", h.Service)
	}
	return &h, nil
}

// Status 拉取账号池状态。
func (c *Client) Status(ctx context.Context) (*Status, error) {
	resp, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("网关拒绝鉴权（401）：请检查 api_key 是否正确")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网关 /status 返回 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("解析 /status 失败: %w", err)
	}
	return &st, nil
}

// Models 拉取模型列表（可能触发网关向上游拉取，耗时较长）。
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("网关拒绝鉴权（401）：请检查 api_key 是否正确")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("网关 /v1/models 返回 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	return out.Data, nil
}

// ChatResult 非流式聊天结果。
type ChatResult struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Model            string `json:"model"`
	FinishReason     string `json:"finish_reason,omitempty"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Raw              string `json:"raw,omitempty"`
}

// Chat 非流式聊天（网关侧本地聚合）。
func (c *Client) Chat(ctx context.Context, body []byte) (*ChatResult, error) {
	resp, err := c.do(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", explainGatewayError(resp.StatusCode, raw))
	}
	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("解析聊天响应失败: %w (body: %s)", err, truncate(string(raw), 200))
	}
	out := &ChatResult{
		Model:            parsed.Model,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
		Raw:              string(raw),
	}
	if len(parsed.Choices) > 0 {
		out.Content = parsed.Choices[0].Message.Content
		out.ReasoningContent = parsed.Choices[0].Message.ReasoningContent
		out.FinishReason = parsed.Choices[0].FinishReason
	}
	return out, nil
}

// Delta 流式聊天的一个增量帧。
type Delta struct {
	Content   string `json:"content,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Usage     *Usage `json:"usage,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Usage token 用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatStream 流式聊天：把网关 SSE 逐帧解析后经 onDelta 回调吐出。
// 返回的 error 只表示传输/协议层失败；上游业务错误以 Delta.Error 形式抵达。
func (c *Client) ChatStream(ctx context.Context, body []byte, onDelta func(Delta) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	if key := c.apiKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// 流式请求不能套用客户端总超时：长思考/长输出会被掐断。
	streamClient := &http.Client{
		Transport: c.http.Transport,
	}
	resp, err := streamClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接网关失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("%s", explainGatewayError(resp.StatusCode, raw))
	}

	sc := bufio.NewScanner(resp.Body)
	// 单帧可能很长（含 reasoning 或 tool_calls），放宽到 1 MiB。
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			return onDelta(Delta{Done: true})
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// 无法解析的帧直接跳过，不影响整体流。
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			if err := onDelta(Delta{Error: chunk.Error.Message}); err != nil {
				return err
			}
			continue
		}
		d := Delta{Usage: chunk.Usage}
		if len(chunk.Choices) > 0 {
			d.Content = chunk.Choices[0].Delta.Content
			d.Reasoning = chunk.Choices[0].Delta.ReasoningContent
			if chunk.Choices[0].FinishReason != "" && chunk.Choices[0].FinishReason != "null" {
				d.Done = true
			}
		}
		if d.Content == "" && d.Reasoning == "" && d.Usage == nil && !d.Done {
			continue
		}
		if err := onDelta(d); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("读取流失败: %w", err)
	}
	return nil
}

// explainGatewayError 把网关的 OpenAI 风格错误体翻译成人话。
func explainGatewayError(status int, raw []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
		msg = env.Error.Message
	} else {
		msg = truncate(string(raw), 200)
	}
	switch status {
	case http.StatusUnauthorized:
		return "网关鉴权失败（401）：api_key 不正确"
	case http.StatusServiceUnavailable:
		return "网关无可用账号（503）：" + msg
	default:
		return fmt.Sprintf("网关返回 HTTP %d：%s", status, msg)
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n]
	}
	return s
}
