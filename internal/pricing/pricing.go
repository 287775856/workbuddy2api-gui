// Package pricing 官方 API 价格表：把网关的 token 用量换算成「如果走官方 API 要花多少钱」。
//
// 设计取态：
//   - **不猜价格**。只内置有权威来源的单价（DeepSeek 官方定价页，含抓取日期），
//     其余模型留空由用户在面板里填——编造价格会让"省了多少钱"看起来精确但实际是错的，
//     比不做更糟。
//   - 价格表持久化到独立文件，官方调价时改配置即可，不随代码更新丢失。
//   - 计价口径分三档（缓存命中输入 / 缓存未命中输入 / 输出），因为缓存命中的输入
//     单价通常只有未命中的几十分之一，混在一起算会严重高估花费。
package pricing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ModelPrice 单个模型的官方单价（单位：元 / 百万 token）。
//
// 三档分开计价是必须的：以 DeepSeek 为例，缓存命中输入 0.04 元而未命中 2 元
// （相差 50 倍），若不区分缓存，一个高命中率的会话会被高估几十倍。
type ModelPrice struct {
	CachedInput float64 `json:"cached_input"` // 缓存命中输入
	MissInput   float64 `json:"miss_input"`   // 缓存未命中输入
	Output      float64 `json:"output"`       // 输出

	// OffPeakRatio 空闲时段价格倍数（DeepSeek 为 0.5，即空闲价是高峰价的一半）。
	// 0 或 1 表示该模型不区分时段。
	OffPeakRatio float64 `json:"off_peak_ratio,omitempty"`

	// Note 备注（如价格来源、口径说明），面板会展示给用户。
	Note string `json:"note,omitempty"`
}

// Priced 报告该条目是否已配置有效价格（全 0 视为未配置）。
func (p ModelPrice) Priced() bool {
	return p.CachedInput > 0 || p.MissInput > 0 || p.Output > 0
}

// Table 价格表：模型名 → 单价。
type Table struct {
	// Models 显式价格（用户可编辑）。
	Models map[string]ModelPrice `json:"models"`

	// Meta 元信息（来源/更新日期），便于判断价格是否过期。
	Source    string `json:"source,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`

	path string
	mu   sync.RWMutex
}

// diskFormat 价格表的磁盘表示。
//
// 独立于 Table 的原因：Table 内嵌 sync.RWMutex，直接序列化/拷贝 Table 会复制锁
// （go vet 报 "copies lock value"）。JSON 字段与 Table 保持一致，保证格式兼容。
type diskFormat struct {
	Models    map[string]ModelPrice `json:"models"`
	Source    string                `json:"source,omitempty"`
	UpdatedAt string                `json:"updated_at,omitempty"`
}

// DeepSeekSource 内置 DeepSeek 价格的来源（面板展示，便于用户核对与更新）。
const DeepSeekSource = "https://api-docs.deepseek.com/zh-cn/quick_start/pricing"

// deepSeekUpdated 内置价格抓取日期。官方调价后此值会过期，面板会据此提示核对。
const deepSeekUpdated = "2026-09-14"

// Default 内置默认价格表。
//
// 只内置 DeepSeek —— 其官方定价页是静态可抓的，价格已逐项核对。
// 其余厂商（智谱/Kimi/混元/MiniMax）定价页为 JS 动态渲染，无法可靠抓取，
// 因此不预设、由用户在面板填写；宁可留空也不编造。
func Default() *Table {
	// DeepSeek 官方价（元/百万 token）。高峰时段为本工作日 9:00-12:00、14:00-18:00，
	// 空闲时段为其余时间，空闲价 = 高峰价 × 0.5。
	flash := ModelPrice{
		CachedInput:  0.04,
		MissInput:    2.0,
		Output:       8.0,
		OffPeakRatio: 0.5,
		Note:         "DeepSeek-V4.1-Flash（高峰价；空闲时段为半价）",
	}
	pro := ModelPrice{
		CachedInput:  0.30,
		MissInput:    9.0,
		Output:       27.0,
		OffPeakRatio: 0.5,
		Note:         "DeepSeek-V4-Pro-0813（高峰价；空闲时段为半价）",
	}
	m := map[string]ModelPrice{}
	// 官方现用名 + 历史名（官方文档说明旧名仍可调用且按 Flash 计费）。
	for _, name := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4.1-flash", "deepseek-v4-flash-vision-exp"} {
		m[name] = flash
	}
	for _, name := range []string{"deepseek-v4-pro", "deepseek-v4-pro-0813"} {
		m[name] = pro
	}
	return &Table{
		Models:    m,
		Source:    DeepSeekSource,
		UpdatedAt: deepSeekUpdated,
	}
}

// New 构建并尝试加载持久化价格表；文件缺失/损坏时用内置默认值。
func New(path string) *Table {
	t := Default()
	t.path = path
	if path != "" {
		t.load()
	}
	return t
}

// load 从磁盘合并价格表（文件中的条目覆盖内置同名条目）。
func (t *Table) load() {
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var disk diskFormat
	if json.Unmarshal(raw, &disk) != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for name, p := range disk.Models {
		t.Models[name] = p
	}
	if disk.Source != "" {
		t.Source = disk.Source
	}
	if disk.UpdatedAt != "" {
		t.UpdatedAt = disk.UpdatedAt
	}
}

// Save 原子写回价格表。用户编辑后调用。
func (t *Table) Save() error {
	if t.path == "" {
		return fmt.Errorf("未配置价格表路径")
	}
	t.mu.RLock()
	// 用独立的序列化结构而不是拷贝 Table 本体 —— Table 内嵌 sync.RWMutex，
	// 按值拷贝会把锁一起复制（go vet 会报 "copies lock value"），
	// 且复制出的锁与本体无关，是典型的误用。
	doc := diskFormat{Models: t.Models, Source: t.Source, UpdatedAt: t.UpdatedAt}
	t.mu.RUnlock()

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if dir := filepath.Dir(t.path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}

// Set 更新单个模型的单价（用户编辑入口）。
func (t *Table) Set(model string, p ModelPrice) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Models == nil {
		t.Models = map[string]ModelPrice{}
	}
	t.Models[model] = p
}

// Delete 删除单个模型的价格（恢复未配置状态）。
func (t *Table) Delete(model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.Models, model)
}

// Models 返回当前价格表的副本（面板展示用）。
func (t *Table) ModelsCopy() map[string]ModelPrice {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]ModelPrice, len(t.Models))
	for k, v := range t.Models {
		out[k] = v
	}
	return out
}

// Resolve 查找模型单价。先精确匹配，再做别名归一化匹配
// （网关的模型名可能是别名，如 deepseek-v4.1-flash ↔ deepseek-flash）。
func (t *Table) Resolve(model string) (ModelPrice, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if p, ok := t.Models[model]; ok && p.Priced() {
		return p, true
	}
	// 归一化后比较：统一小写、去分隔符（. - _），便于 deepseek-v4.1-flash 命中 deepseek-v4.1.flash 之类的差异。
	norm := normalize(model)
	for name, p := range t.Models {
		if normalize(name) == norm && p.Priced() {
			return p, true
		}
	}
	return ModelPrice{}, false
}

// normalize 归一化模型名用于宽松匹配。
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(s)
}

// TimeMode 计价时段。
type TimeMode string

const (
	// ModePeak 高峰时段价（默认，估算偏保守）。
	ModePeak TimeMode = "peak"
	// ModeOffPeak 空闲时段价。
	ModeOffPeak TimeMode = "offpeak"
)

// NormalizeTimeMode 规范化时段参数。
func NormalizeTimeMode(s string) TimeMode {
	if strings.EqualFold(strings.TrimSpace(s), string(ModeOffPeak)) {
		return ModeOffPeak
	}
	return ModePeak
}

// Cost 单个模型的换算结果。
type Cost struct {
	Model string `json:"model"`

	// Priced 是否有价格（false 时各金额为 0，界面应提示去配置）。
	Priced bool `json:"priced"`
	// Note 价格备注。
	Note string `json:"note,omitempty"`

	// 三档花费（元）。
	CachedInputCost float64 `json:"cached_input_cost"`
	MissInputCost   float64 `json:"miss_input_cost"`
	OutputCost      float64 `json:"output_cost"`
	// Total 合计（元）。
	Total float64 `json:"total"`

	// 参与计算的 token 数（与统计口径一致，便于核对）。
	CachedInputTokens int64 `json:"cached_input_tokens"`
	MissInputTokens   int64 `json:"miss_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

// Usage 计价所需的 token 用量（从统计条目提取，避免本包依赖 metrics 包）。
type Usage struct {
	PromptTokens     int64
	CacheHitTokens   int64
	CacheMissTokens  int64
	CompletionTokens int64
}

// Compute 按价格表把用量换算成官方 API 花费。
//
// 缓存未命中 token 的取值：优先用上游明确上报的 miss；若为 0 但 prompt 大于 hit，
// 则按 prompt-hit 补齐（部分模型只报 prompt 与 hit，不报 miss）。这样即使上游
// 缺少缓存明细，也不会把整段输入误当成命中（那会严重低估花费）。
func (t *Table) Compute(model string, u Usage, mode TimeMode) Cost {
	c := Cost{Model: model}
	p, ok := t.Resolve(model)
	if !ok {
		return c
	}
	c.Priced = true
	c.Note = p.Note

	hit := u.CacheHitTokens
	if hit < 0 {
		hit = 0
	}
	miss := u.CacheMissTokens
	if fallback := u.PromptTokens - hit; fallback > miss {
		miss = fallback
	}
	if miss < 0 {
		miss = 0
	}
	out := u.CompletionTokens
	if out < 0 {
		out = 0
	}

	// 空闲时段按倍数折算（DeepSeek 空闲价 = 高峰价 × 0.5）。
	ratio := 1.0
	if mode == ModeOffPeak && p.OffPeakRatio > 0 {
		ratio = p.OffPeakRatio
	}

	const perMillion = 1_000_000
	c.CachedInputTokens = hit
	c.MissInputTokens = miss
	c.OutputTokens = out
	c.CachedInputCost = float64(hit) / perMillion * p.CachedInput * ratio
	c.MissInputCost = float64(miss) / perMillion * p.MissInput * ratio
	c.OutputCost = float64(out) / perMillion * p.Output * ratio
	c.Total = c.CachedInputCost + c.MissInputCost + c.OutputCost
	return c
}

// Unpriced 返回价格表中没有单价的模型名（面板提示"去配置"用）。
func (t *Table) Unpriced(models []string) []string {
	var out []string
	for _, m := range models {
		if _, ok := t.Resolve(m); !ok {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
