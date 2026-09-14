package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestComputeDeepSeekFlash 按 DeepSeek 官方价换算，三档分开计价。
//
// 用整数便于手算核对：命中 1M / 未命中 1M / 输出 1M（高峰价）。
// 预期 = 0.04 + 2.00 + 8.00 = 10.04 元。
func TestComputeDeepSeekFlash(t *testing.T) {
	tb := Default()
	c := tb.Compute("deepseek-v4.1-flash", Usage{
		PromptTokens:     2_000_000,
		CacheHitTokens:   1_000_000,
		CacheMissTokens:  1_000_000,
		CompletionTokens: 1_000_000,
	}, ModePeak)

	if !c.Priced {
		t.Fatal("内置应有 DeepSeek 价格")
	}
	if c.CachedInputCost != 0.04 {
		t.Errorf("命中输入花费 = %v, want 0.04", c.CachedInputCost)
	}
	if c.MissInputCost != 2.0 {
		t.Errorf("未命中输入花费 = %v, want 2.0", c.MissInputCost)
	}
	if c.OutputCost != 8.0 {
		t.Errorf("输出花费 = %v, want 8.0", c.OutputCost)
	}
	if c.Total != 10.04 {
		t.Errorf("合计 = %v, want 10.04", c.Total)
	}
}

// TestOffPeakHalfPrice 空闲时段价为高峰价的一半。
func TestOffPeakHalfPrice(t *testing.T) {
	tb := Default()
	u := Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, CompletionTokens: 1_000_000}

	peak := tb.Compute("deepseek-v4.1-flash", u, ModePeak)
	off := tb.Compute("deepseek-v4.1-flash", u, ModeOffPeak)

	// 未命中 2.0 → 空闲 1.0；输出 8.0 → 4.0。
	if off.MissInputCost != 1.0 {
		t.Errorf("空闲未命中 = %v, want 1.0", off.MissInputCost)
	}
	if off.OutputCost != 4.0 {
		t.Errorf("空闲输出 = %v, want 4.0", off.OutputCost)
	}
	if off.Total >= peak.Total {
		t.Errorf("空闲价应低于高峰价: off=%v peak=%v", off.Total, peak.Total)
	}
}

// TestCacheHitMuchCheaperThanMiss 缓存命中的输入单价必须远低于未命中
// （DeepSeek 相差 50 倍）。这是分档计价的核心意义：若把命中当未命中算，
// 高命中率会话会被高估几十倍。
func TestCacheHitMuchCheaperThanMiss(t *testing.T) {
	tb := Default()
	// 全部命中 vs 全部未命中（同为 1M 输入）。
	allHit := tb.Compute("deepseek-v4.1-flash", Usage{
		PromptTokens: 1_000_000, CacheHitTokens: 1_000_000,
	}, ModePeak)
	allMiss := tb.Compute("deepseek-v4.1-flash", Usage{
		PromptTokens: 1_000_000, CacheMissTokens: 1_000_000,
	}, ModePeak)

	if allHit.Total >= allMiss.Total {
		t.Errorf("全命中(%v)应远低于全未命中(%v)", allHit.Total, allMiss.Total)
	}
	if ratio := allMiss.Total / allHit.Total; ratio < 10 {
		t.Errorf("命中/未命中价差应显著（>10x），实际 %.1fx", ratio)
	}
}

// TestMissFallbackFromPrompt 上游只报 prompt 与 hit、不报 miss 时，
// 必须用 prompt-hit 补齐未命中，否则会把输入全当命中而严重低估花费。
func TestMissFallbackFromPrompt(t *testing.T) {
	tb := Default()
	c := tb.Compute("deepseek-v4.1-flash", Usage{
		PromptTokens:    1_000_000,
		CacheHitTokens:  200_000,
		CacheMissTokens: 0, // 上游未报
	}, ModePeak)

	if c.MissInputTokens != 800_000 {
		t.Errorf("未命中应回落为 prompt-hit = 800000, 得到 %d", c.MissInputTokens)
	}
	// 0.2M × 0.04 + 0.8M × 2.0 = 0.008 + 1.6 = 1.608
	if c.Total < 1.6 || c.Total > 1.62 {
		t.Errorf("合计 = %v, want ≈1.608", c.Total)
	}
}

// TestMissNotOverriddenWhenLarger 上游明确报的 miss 更大时应以其为准
// （不能因为 prompt-hit 更小就覆盖掉真实值）。
func TestMissNotOverriddenWhenLarger(t *testing.T) {
	tb := Default()
	c := tb.Compute("deepseek-v4.1-flash", Usage{
		PromptTokens:    100_000, // 小于 hit+miss（数据不一致场景）
		CacheHitTokens:  50_000,
		CacheMissTokens: 200_000,
	}, ModePeak)

	if c.MissInputTokens != 200_000 {
		t.Errorf("应采信上游上报的 miss=200000, 得到 %d", c.MissInputTokens)
	}
}

// TestUnpricedModel 没配价格的模型应返回 Priced=false 且金额为 0
// （界面据此提示"去配置"），绝不能猜一个价格。
func TestUnpricedModel(t *testing.T) {
	tb := Default()
	c := tb.Compute("glm-5.3", Usage{
		PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, CompletionTokens: 1_000_000,
	}, ModePeak)

	if c.Priced {
		t.Error("未配置价格的模型不应 Priced=true（不能编造价格）")
	}
	if c.Total != 0 {
		t.Errorf("未配置时金额应为 0, 得到 %v", c.Total)
	}
}

// TestSetAndResolve 用户填的价格应生效并可解析。
func TestSetAndResolve(t *testing.T) {
	tb := Default()
	tb.Set("glm-5.3", ModelPrice{CachedInput: 1, MissInput: 10, Output: 30, Note: "用户填写"})

	c := tb.Compute("glm-5.3", Usage{
		PromptTokens: 2_000_000, CacheHitTokens: 1_000_000,
		CacheMissTokens: 1_000_000, CompletionTokens: 1_000_000,
	}, ModePeak)
	if !c.Priced {
		t.Fatal("填了价格后应 Priced=true")
	}
	if c.Total != 41 { // 1 + 10 + 30
		t.Errorf("合计 = %v, want 41", c.Total)
	}
	if c.Note != "用户填写" {
		t.Errorf("备注 = %q", c.Note)
	}
}

// TestResolveLooseMatching 模型名归一化匹配（网关用别名，价格表用官方名）。
func TestResolveLooseMatching(t *testing.T) {
	tb := Default()
	// 内置键含 deepseek-v4.1-flash 与 deepseek-flash，二者应都能解析。
	for _, name := range []string{"deepseek-v4.1-flash", "deepseek-flash", "deepseek-v4-flash"} {
		if _, ok := tb.Resolve(name); !ok {
			t.Errorf("%s 应能解析到价格", name)
		}
	}
	// 大小写/分隔符差异也应命中。
	if _, ok := tb.Resolve("DeepSeek-V4.1-Flash"); !ok {
		t.Error("大小写差异应能命中")
	}
}

// TestPersistence 用户编辑的价格应落盘并在重建后生效。
func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "pricing.json")

	tb := New(fp)
	tb.Set("glm-5.3", ModelPrice{CachedInput: 0.5, MissInput: 5, Output: 15, Note: "自填"})
	if err := tb.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("价格表文件应已生成: %v", err)
	}

	// 重建：内置默认 + 磁盘覆盖。
	tb2 := New(fp)
	if _, ok := tb2.Resolve("glm-5.3"); !ok {
		t.Fatal("重启后自填价格丢失")
	}
	// 内置的 DeepSeek 价应仍在（磁盘只覆盖同名条目）。
	if _, ok := tb2.Resolve("deepseek-v4.1-flash"); !ok {
		t.Error("内置价格不应被磁盘文件覆盖丢失")
	}
}

// TestPersistenceCorruptFileUsesDefaults 文件损坏时回落内置默认，不 panic。
func TestPersistenceCorruptFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "pricing.json")
	if err := os.WriteFile(fp, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	tb := New(fp)
	if _, ok := tb.Resolve("deepseek-v4.1-flash"); !ok {
		t.Error("损坏文件应回落内置默认价")
	}
}

// TestDelete 删除后该模型回到未配置状态。
func TestDelete(t *testing.T) {
	tb := Default()
	tb.Delete("deepseek-v4.1-flash")
	if _, ok := tb.Resolve("deepseek-v4.1-flash"); ok {
		t.Error("删除后不应再能解析")
	}
}

// TestSinceNoPathSaveFails 未配置路径时保存应明确报错（而不是静默丢弃）。
func TestSaveWithoutPathFails(t *testing.T) {
	tb := Default() // path 为空
	if err := tb.Save(); err == nil {
		t.Error("未配置路径时保存应报错")
	}
}

// TestNormalizeTimeMode 时段参数容错。
func TestNormalizeTimeMode(t *testing.T) {
	cases := map[string]TimeMode{
		"":           ModePeak,
		"peak":       ModePeak,
		"offpeak":    ModeOffPeak,
		"OFFPEAK":    ModeOffPeak,
		"  offPeak ": ModeOffPeak,
		"bogus":      ModePeak,
	}
	for in, want := range cases {
		if got := NormalizeTimeMode(in); got != want {
			t.Errorf("NormalizeTimeMode(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestUnpriced 列出没配价格的模型。
func TestUnpriced(t *testing.T) {
	tb := Default()
	got := tb.Unpriced([]string{"glm-5.3", "deepseek-v4.1-flash", "hy3"})
	if len(got) != 2 || got[0] != "glm-5.3" || got[1] != "hy3" {
		t.Errorf("未配置列表 = %v, want [glm-5.3 hy3]", got)
	}
}

// TestZeroUsageNoCost 零用量应为 0 花费（不发生 NaN）。
func TestZeroUsageNoCost(t *testing.T) {
	tb := Default()
	c := tb.Compute("deepseek-v4.1-flash", Usage{}, ModePeak)
	if c.Total != 0 {
		t.Errorf("零用量花费应为 0, 得到 %v", c.Total)
	}
}

// TestSaveDoesNotCopyMutex 回归：Save 不得按值拷贝 Table（内含 sync.RWMutex）。
// 之前用 Table{...} 构造快照会复制锁，go vet 报 "copies lock value"；
// 现改用独立 diskFormat。此测试锁定磁盘格式仍兼容（字段名不变）。
func TestSaveLoadRoundTripFormat(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "pricing.json")

	tb := New(fp)
	tb.Set("custom-model", ModelPrice{CachedInput: 0.1, MissInput: 1, Output: 3, Note: "n"})
	if err := tb.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 磁盘 JSON 的键名必须是 models/source/updated_at（与旧格式兼容）。
	raw, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("磁盘内容应为合法 JSON: %v", err)
	}
	for _, k := range []string{"models", "source", "updated_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("磁盘格式缺少字段 %q（会破坏向后兼容）", k)
		}
	}

	// 重新加载应能读回自填价格。
	tb2 := New(fp)
	if _, ok := tb2.Resolve("custom-model"); !ok {
		t.Error("重新加载后自填价格丢失")
	}
}
