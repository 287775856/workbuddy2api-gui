// StatsPage.tsx 请求统计：按模型分开统计首字延迟、吞吐、缓存命中、输入输出与扣费。
//
// 数据源是网关的 /v1/stats —— 网关是所有流量的必经点，因此这里看到的**包含**
// 绕过本面板的其他客户端（比如你自己的工具/脚本）的调用。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../api'
import type { ModelCost, ModelStat, ModelPrice, SessionInfo, StatsResponse } from '../types'
import { Alert, Empty, fmtDuration, fmtISO, fmtNum, Modal, Spinner } from '../ui'

/** 数值格式化：大数用千分位，小数保留位数。 */
function fmtMs(v: number): string {
  if (!v) return '—'
  return v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`
}
function fmtRate(v: number): string {
  return v ? v.toFixed(1) : '—'
}
function fmtPct(v: number): string {
  if (!v) return '0%'
  return `${(v * 100).toFixed(1)}%`
}
function fmtCredit(v: number): string {
  return v ? v.toFixed(4) : '0'
}
/** 大 token 数缩写（1.2M / 345.6K / 123）。 */
function fmtTok(v: number): string {
  if (!v) return '0'
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}K`
  return String(v)
}

/** 缓存命中率配色：越高越省（命中部分通常便宜得多）。 */
function hitTone(rate: number): string {
  if (rate >= 0.5) return 'text-ok'
  if (rate >= 0.1) return 'text-warn'
  return 'text-dim'
}

export default function StatsPage({ session }: { session: SessionInfo }) {
  const [resp, setResp] = useState<StatsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [sortKey, setSortKey] = useState<keyof ModelStat>('requests')
  const [resetting, setResetting] = useState(false)
  // timeMode 影响官方价换算：DeepSeek 空闲时段是高峰价的一半。
  const [timeMode, setTimeMode] = useState<'peak' | 'offpeak'>('peak')
  // 价格编辑弹窗：null = 关闭；否则为正在编辑的模型名
  const [editingModel, setEditingModel] = useState<string | null>(null)

  const stats = resp?.stats ?? null

  const load = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true)
      try {
        const s = await api.stats(timeMode)
        setResp(s)
        setError(null)
      } catch (err) {
        setError(err instanceof ApiError ? err.message : '加载统计失败')
      } finally {
        setLoading(false)
      }
    },
    [timeMode],
  )

  useEffect(() => {
    void load()
  }, [load])

  // 自动刷新：统计是累计值，10 秒一次足够看出趋势。
  useEffect(() => {
    if (!autoRefresh) return
    const timer = setInterval(() => void load(true), 10_000)
    return () => clearInterval(timer)
  }, [autoRefresh, load])

  const doReset = async () => {
    setResetting(true)
    setNotice(null)
    try {
      const r = await api.resetStats()
      setNotice(r.message || '统计已重置')
      await load(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '重置失败')
    } finally {
      setResetting(false)
    }
  }

  // 排序后的模型列表（默认按请求数降序，热点模型在最上面）。
  const models = useMemo(() => {
    const list = [...(stats?.models ?? [])]
    list.sort((a, b) => {
      const av = a[sortKey]
      const bv = b[sortKey]
      if (typeof av === 'number' && typeof bv === 'number') return bv - av
      return String(av).localeCompare(String(bv))
    })
    return list
  }, [stats, sortKey])

  if (loading && !stats) return <Spinner label="正在加载统计…" />

  // 统计不可用：两种成因表现不同，提示也要区别 ——
  //   1) 网关有该端点但关了开关（metrics_enabled=false），提示去配置里打开；
  //   2) 网关根本没有该端点（官方上游版本），提示换用配套网关，别让人去翻配置。
  if (stats && !stats.enabled) {
    const noEndpoint = stats.message?.includes('/v1/stats') ?? false
    return (
      <>
        <div className="page-head">
          <div>
            <h1>请求统计</h1>
            <p>按模型聚合的首字延迟、吞吐、缓存命中与扣费</p>
          </div>
        </div>
        <Alert kind="warn">
          <strong>{noEndpoint ? '当前网关不支持请求统计。' : '网关未启用统计。'}</strong>
          <div style={{ marginTop: 4 }}>
            {stats.message ||
              (noEndpoint
                ? '本页需要网关提供 /v1/stats 端点，官方上游版本暂未包含。面板其余功能不受影响。'
                : '请在网关配置中设置 server.metrics_enabled=true')}
          </div>
        </Alert>
      </>
    )
  }

  const t = stats?.total

  return (
    <>
      <div className="page-head">
        <div>
          <h1>请求统计</h1>
          <p>
            统计<strong>所有</strong>经过网关的请求（含绕过本面板的客户端），按模型分开
            {stats && <span className="text-faint"> · 已运行 {fmtDuration(stats.uptime_sec)}</span>}
          </p>
        </div>
        <div className="page-actions">
          <label className="checkbox">
            <input type="checkbox" checked={autoRefresh} onChange={(e) => setAutoRefresh(e.target.checked)} />
            自动刷新（10s）
          </label>
          <button className="btn" onClick={() => void load()} disabled={loading}>
            {loading ? <Spinner /> : '🔄'} 刷新
          </button>
          <button
            className="btn btn-danger"
            onClick={() => void doReset()}
            disabled={resetting || session.read_only}
            title={session.read_only ? '只读模式' : '清空累计统计，便于观察之后的增量'}
          >
            {resetting ? <Spinner /> : '🧹'} 重置统计
          </button>
        </div>
      </div>

      {notice && (
        <Alert kind="ok" onClose={() => setNotice(null)}>
          {notice}
        </Alert>
      )}
      {error && (
        <Alert kind="error" onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      {/* 汇总卡片 */}
      {t && (
        <div className="grid grid-stats" style={{ marginBottom: 16 }}>
          <Stat
            label="总请求"
            value={fmtNum(t.requests)}
            sub={`成功 ${fmtNum(t.success)}${t.failed ? ` · 失败 ${fmtNum(t.failed)}` : ''} · 流式 ${fmtNum(t.streaming)}`}
          />
          <Stat
            label="平均首字"
            value={fmtMs(t.avg_ttfb_ms)}
            sub={`平均耗时 ${fmtMs(t.avg_latency_ms)}`}
            tone={t.avg_ttfb_ms > 5000 ? 'warn' : undefined}
          />
          <Stat
            label="生成吞吐"
            value={t.tokens_per_sec ? `${fmtRate(t.tokens_per_sec)} tok/s` : '—'}
            sub="输出 token / 生成秒数"
          />
          <Stat
            label="输入 / 输出"
            value={`${fmtTok(t.prompt_tokens)} / ${fmtTok(t.completion_tokens)}`}
            sub={`合计 ${fmtTok(t.total_tokens)} token`}
          />
          <Stat
            label="缓存命中率"
            value={fmtPct(t.cache_hit_rate)}
            sub={`命中 ${fmtTok(t.cache_hit_tokens)} · 未命中 ${fmtTok(t.cache_miss_tokens)}`}
            tone={t.cache_hit_rate >= 0.5 ? 'ok' : undefined}
          />
          <Stat label="累计扣费（积分）" value={fmtCredit(t.credit)} sub={`平均每请求 ${fmtCredit(t.credit_per_req)} 积分`} />
          {resp?.total?.priced && (
            <Stat
              label={`官方 API 应付（${timeMode === 'peak' ? '高峰' : '空闲'}价）`}
              value={`¥${resp.total.total.toFixed(4)}`}
              sub={
                resp.unpriced && resp.unpriced.length > 0
                  ? `${resp.unpriced.length} 个模型未配价，未计入`
                  : '按各模型官方单价分别换算后求和'
              }
              tone="warn"
            />
          )}
        </div>
      )}

      {/* 官方价换算说明 */}
      {resp && (
        <div className="card">
          <div className="card-head">
            <h2>官方 API 价格换算</h2>
            <div className="page-actions">
              <span className="hint">计价时段</span>
              <select
                value={timeMode}
                onChange={(e) => setTimeMode(e.target.value as 'peak' | 'offpeak')}
                style={{ width: 160 }}
                title="DeepSeek 空闲时段价为高峰价的一半（高峰：工作日 9-12、14-18 点）"
              >
                <option value="peak">高峰时段价</option>
                <option value="offpeak">空闲时段价（半价）</option>
              </select>
              <button
                className="btn btn-sm"
                onClick={() => setEditingModel('__new__')}
                disabled={!resp.pricing.editable || session.read_only}
                title={
                  !resp.pricing.editable
                    ? '服务端未配置 pricing_file，无法保存价格'
                    : session.read_only
                      ? '只读模式'
                      : '添加或修改模型单价'
                }
              >
                ✏️ 编辑价格
              </button>
            </div>
          </div>

          <div className="grid grid-stats" style={{ marginBottom: 14 }}>
            <div className="stat">
              <div className="stat-label">官方应付（合计）</div>
              <div className="stat-value small text-warn">¥{resp.total.total.toFixed(4)}</div>
              <div className="stat-sub">命中 ¥{resp.total.cached_input_cost.toFixed(4)} · 未命中 ¥{resp.total.miss_input_cost.toFixed(4)} · 输出 ¥{resp.total.output_cost.toFixed(4)}</div>
            </div>
            <div className="stat">
              <div className="stat-label">网关累计计费</div>
              <div className="stat-value small">{fmtCredit(t?.credit ?? 0)}</div>
              <div className="stat-sub">上游返回的 credit（单位：账号积分，非元）</div>
            </div>
          </div>

          <Alert kind="info">
            两个数字<strong>单位不同，不做相减</strong>：官方应付是<strong>元</strong>（按厂商定价页），
            网关计费是账号的<strong>积分</strong>（你的套餐是 500/1500/100 积分制）。
            两者量纲不同，相减得出的"差额"没有意义，因此这里只并列展示。
            想知道积分与人民币的兑换比例，请以官方充值页为准。
          </Alert>

          {resp.unpriced && resp.unpriced.length > 0 && (
            <Alert kind="info">
              以下模型<strong>未配置官方单价</strong>，未计入换算（点「编辑价格」填写后即可看到）：
              <div style={{ marginTop: 5, display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                {resp.unpriced.map((m) => (
                  <button
                    key={m}
                    className="btn btn-sm"
                    onClick={() => setEditingModel(m)}
                    disabled={!resp.pricing.editable || session.read_only}
                    title="点击填写该模型的单价"
                  >
                    {m} <span className="text-faint">＋</span>
                  </button>
                ))}
              </div>
            </Alert>
          )}

          {resp.pricing.source && (
            <div className="desc" style={{ marginTop: 10 }}>
              内置价格来源：<a href={resp.pricing.source} target="_blank" rel="noopener noreferrer">{resp.pricing.source}</a>
              {resp.pricing.updated_at && <span className="text-faint">（抓取于 {resp.pricing.updated_at}，官方调价后请点「编辑价格」更新）</span>}
            </div>
          )}
        </div>
      )}

      {/* 按模型明细 */}
      <div className="card">
        <div className="card-head">
          <h2>按模型明细</h2>
          <div className="page-actions">
            <span className="hint">排序</span>
            <select
              value={String(sortKey)}
              onChange={(e) => setSortKey(e.target.value as keyof ModelStat)}
              style={{ width: 150 }}
            >
              <option value="requests">请求数</option>
              <option value="avg_ttfb_ms">首字延迟</option>
              <option value="tokens_per_sec">吞吐</option>
              <option value="cache_hit_rate">缓存命中率</option>
              <option value="credit">扣费</option>
              <option value="completion_tokens">输出 token</option>
            </select>
          </div>
        </div>

        {models.length === 0 ? (
          <Empty>
            还没有统计数据。
            <div style={{ marginTop: 6, fontSize: 12 }}>
              向网关发一次请求（可用「聊天测试」页）后即可看到。
            </div>
          </Empty>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>模型</th>
                  <th className="num">请求</th>
                  <th className="num">首字</th>
                  <th className="num">吞吐</th>
                  <th className="num">输入</th>
                  <th className="num">输出</th>
                  <th className="num">缓存命中</th>
                  <th className="num">扣费</th>
                  <th className="num">官方价</th>
                  <th>最近</th>
                </tr>
              </thead>
              <tbody>
                {models.map((m) => (
                  <tr key={m.model}>
                    <td>
                      <div className="mono" style={{ fontSize: 12.5 }}>
                        {m.model}
                      </div>
                      {m.failed > 0 && (
                        <div className="text-danger" style={{ fontSize: 11 }}>
                          失败 {m.failed}
                        </div>
                      )}
                    </td>
                    <td className="num">
                      {fmtNum(m.requests)}
                      {m.streaming > 0 && (
                        <div className="text-faint" style={{ fontSize: 11 }}>
                          流式 {m.streaming}
                        </div>
                      )}
                    </td>
                    <td className="num">{fmtMs(m.avg_ttfb_ms)}</td>
                    <td className="num">
                      {fmtRate(m.tokens_per_sec)}
                      <div className="text-faint" style={{ fontSize: 11 }}>
                        tok/s
                      </div>
                    </td>
                    <td className="num" title={fmtNum(m.prompt_tokens)}>
                      {fmtTok(m.prompt_tokens)}
                    </td>
                    <td className="num" title={fmtNum(m.completion_tokens)}>
                      {fmtTok(m.completion_tokens)}
                    </td>
                    <td className="num">
                      <span className={hitTone(m.cache_hit_rate)}>{fmtPct(m.cache_hit_rate)}</span>
                      <div className="text-faint" style={{ fontSize: 11 }} title={`命中 ${fmtNum(m.cache_hit_tokens)} / 未命中 ${fmtNum(m.cache_miss_tokens)}`}>
                        {fmtTok(m.cache_hit_tokens)} hit
                      </div>
                    </td>
                    <td className="num">
                      {fmtCredit(m.credit)}
                      <div className="text-faint" style={{ fontSize: 11 }}>
                        {fmtCredit(m.credit_per_req)}/次
                      </div>
                    </td>
                    <td className="num">
                      {(() => {
                        const c: ModelCost | undefined = resp?.costs?.[m.model]
                        if (!c?.priced) {
                          return (
                            <button
                              className="btn btn-sm btn-ghost"
                              onClick={() => setEditingModel(m.model)}
                              disabled={!resp?.pricing.editable || session.read_only}
                              title="未配置官方单价，点击填写"
                              style={{ padding: '0 6px', fontSize: 11 }}
                            >
                              未配置 ＋
                            </button>
                          )
                        }
                        return (
                          <>
                            <span className="text-warn">¥{c.total.toFixed(4)}</span>
                            <div
                              className="text-faint"
                              style={{ fontSize: 11 }}
                              title={`命中 ¥${c.cached_input_cost.toFixed(4)} / 未命中 ¥${c.miss_input_cost.toFixed(4)} / 输出 ¥${c.output_cost.toFixed(4)}`}
                            >
                              {c.output_cost > 0 || c.miss_input_cost > 0 || c.cached_input_cost > 0
                                ? `出 ¥${c.output_cost.toFixed(3)}`
                                : '—'}
                            </div>
                          </>
                        )
                      })()}
                    </td>
                    <td className="text-dim" style={{ fontSize: 12 }}>
                      {fmtISO(m.last_seen)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-head">
          <h2>指标说明</h2>
        </div>
        <dl className="kv">
          <dt>首字延迟</dt>
          <dd>请求发出到收到第一个 token 的时间（TTFB）。只对流式请求有意义，非流式显示为 —。</dd>
          <dt>吞吐</dt>
          <dd>输出 token ÷ 生成秒数（已剔除首字等待），反映模型的真实出字速度。</dd>
          <dt>缓存命中</dt>
          <dd>
            上游 prompt cache 的命中比例。命中的输入 token 计费远低于未命中，
            所以这个数字直接关系到实际花费 —— 同一会话反复追问同一长上下文时命中率会很高。
          </dd>
          <dt>扣费</dt>
          <dd>
            上游返回的 credit 累计（非估算）。<strong>单位是账号积分</strong>（套餐按 500/1500/100 积分计），
            与「官方应付」的<strong>元</strong>不是同一量纲，故两者只并列展示、不做相减。
          </dd>
          <dt>官方应付</dt>
          <dd>
            按厂商官网定价（元/百万 token）把本模型的 token 用量折算成"如果直接走官方 API 要花多少钱"。
            三档分开计价：缓存命中输入、未命中输入、输出 —— 因为命中价通常远低于未命中
            （DeepSeek 相差 50 倍），混算会严重高估。单价可在「编辑价格」里按官方定价页填写。
          </dd>
          <dt>统计范围</dt>
          <dd>
            网关是所有流量的必经点，因此这里<strong>包含其他客户端</strong>（脚本、第三方工具）的调用，
            不限于本面板发起的请求。统计持久化在网关的 data 目录，重启不丢。
          </dd>
          <dt>统计起点</dt>
          <dd>{stats ? fmtISO(stats.since) : '—'}</dd>
        </dl>
      </div>

      {editingModel && resp && (
        <PriceEditor
          model={editingModel === '__new__' ? '' : editingModel}
          existing={resp.pricing.models?.[editingModel] ?? undefined}
          suggestions={
            // 待填模型优先给"统计里有用量但未配价"的，其次给网关模型列表里的
            editingModel === '__new__'
              ? [...new Set([...(resp.unpriced ?? []), ...(resp.stats.models ?? []).map((m) => m.model)])]
              : []
          }
          onClose={() => setEditingModel(null)}
          onSaved={async (msg) => {
            setEditingModel(null)
            setNotice(msg)
            await load(true)
          }}
        />
      )}
    </>
  )
}

/** PriceEditor 编辑单个模型的官方单价（元/百万 token）。 */
function PriceEditor({
  model: initialModel,
  existing,
  suggestions,
  onClose,
  onSaved,
}: {
  model: string
  existing?: ModelPrice
  suggestions: string[]
  onClose: () => void
  onSaved: (msg: string) => Promise<void>
}) {
  const [model, setModel] = useState(initialModel)
  const [cached, setCached] = useState(existing ? String(existing.cached_input) : '')
  const [miss, setMiss] = useState(existing ? String(existing.miss_input) : '')
  const [output, setOutput] = useState(existing ? String(existing.output) : '')
  const [offPeak, setOffPeak] = useState(existing?.off_peak_ratio ? String(existing.off_peak_ratio) : '')
  const [note, setNote] = useState(existing?.note ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    setError(null)
    if (!model.trim()) {
      setError('模型名不能为空')
      return
    }
    const num = (s: string) => (s.trim() === '' ? 0 : Number(s))
    const c = num(cached)
    const mi = num(miss)
    const o = num(output)
    if ([c, mi, o].some((v) => Number.isNaN(v) || v < 0)) {
      setError('单价必须是非负数字')
      return
    }
    if (c === 0 && mi === 0 && o === 0) {
      setError('至少填写一个非零单价（全 0 会被视为未配置）')
      return
    }
    setBusy(true)
    try {
      const r = await api.savePrice({
        model: model.trim(),
        cached_input: c,
        miss_input: mi,
        output: o,
        off_peak_ratio: num(offPeak),
        note: note.trim(),
      })
      await onSaved(r.message || '价格已保存')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={initialModel ? `编辑价格 · ${initialModel}` : '添加模型价格'}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn btn-primary" onClick={() => void submit()} disabled={busy}>
            {busy ? <Spinner /> : null}
            保存
          </button>
        </>
      }
    >
      {error && <Alert kind="error">{error}</Alert>}
      <div className="field">
        <label>模型名（填网关里的模型 ID）</label>
        <input
          type="text"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder="例如 glm-5.3"
          list="price-model-suggestions"
          disabled={!!initialModel}
        />
        {suggestions.length > 0 && (
          <>
            <datalist id="price-model-suggestions">
              {suggestions.map((s) => (
                <option key={s} value={s} />
              ))}
            </datalist>
            <div className="desc">
              待配置：
              {suggestions.slice(0, 8).map((s) => (
                <button
                  key={s}
                  className="btn btn-sm btn-ghost"
                  style={{ padding: '0 5px', fontSize: 11 }}
                  onClick={() => setModel(s)}
                >
                  {s}
                </button>
              ))}
            </div>
          </>
        )}
      </div>

      <div className="row">
        <div className="field" style={{ flex: 1 }}>
          <label>缓存命中输入（元/百万）</label>
          <input type="text" value={cached} onChange={(e) => setCached(e.target.value)} placeholder="0.04" />
        </div>
        <div className="field" style={{ flex: 1 }}>
          <label>缓存未命中输入（元/百万）</label>
          <input type="text" value={miss} onChange={(e) => setMiss(e.target.value)} placeholder="2" />
        </div>
        <div className="field" style={{ flex: 1 }}>
          <label>输出（元/百万）</label>
          <input type="text" value={output} onChange={(e) => setOutput(e.target.value)} placeholder="8" />
        </div>
      </div>

      <div className="field">
        <label>空闲时段价倍数（可选）</label>
        <input type="text" value={offPeak} onChange={(e) => setOffPeak(e.target.value)} placeholder="留空 = 不区分时段；DeepSeek 填 0.5" />
        <div className="desc">用于"空闲时段价"换算。填 0.5 表示空闲价是高峰价的一半。</div>
      </div>

      <div className="field" style={{ marginBottom: 0 }}>
        <label>备注（可选）</label>
        <input type="text" value={note} onChange={(e) => setNote(e.target.value)} placeholder="价格来源 / 口径说明" />
      </div>

      <div className="desc" style={{ marginTop: 12 }}>
        单价单位是<strong>元 / 百万 token</strong>，请以厂商官网定价页为准。
        缓存命中价通常远低于未命中（DeepSeek 相差 50 倍），分开填写才能算准。
      </div>
    </Modal>
  )
}

function Stat({
  label,
  value,
  sub,
  tone,
}: {
  label: string
  value: string
  sub?: string
  tone?: 'ok' | 'warn' | 'danger'
}) {
  const cls = tone === 'ok' ? 'text-ok' : tone === 'warn' ? 'text-warn' : tone === 'danger' ? 'text-danger' : ''
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className={`stat-value small ${cls}`}>{value}</div>
      {sub && <div className="stat-sub">{sub}</div>}
    </div>
  )
}
