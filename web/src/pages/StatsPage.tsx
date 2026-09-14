// StatsPage.tsx 请求统计：按模型分开统计首字延迟、吞吐、缓存命中、输入输出与扣费。
//
// 数据源是网关的 /v1/stats —— 网关是所有流量的必经点，因此这里看到的**包含**
// 绕过本面板的其他客户端（比如你自己的工具/脚本）的调用。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../api'
import type { ModelStat, SessionInfo, Stats } from '../types'
import { Alert, Empty, fmtDuration, fmtISO, fmtNum, Spinner } from '../ui'

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
  const [stats, setStats] = useState<Stats | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [sortKey, setSortKey] = useState<keyof ModelStat>('requests')
  const [resetting, setResetting] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const s = await api.stats()
      setStats(s)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '加载统计失败')
    } finally {
      setLoading(false)
    }
  }, [])

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

  // 统计未启用（服务端 metrics_enabled=false）。
  if (stats && !stats.enabled) {
    return (
      <>
        <div className="page-head">
          <div>
            <h1>请求统计</h1>
            <p>按模型聚合的首字延迟、吞吐、缓存命中与扣费</p>
          </div>
        </div>
        <Alert kind="warn">
          <strong>网关未启用统计。</strong>
          <div style={{ marginTop: 4 }}>{stats.message || '请在网关配置中设置 server.metrics_enabled=true'}</div>
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
          <Stat label="累计扣费" value={fmtCredit(t.credit)} sub={`平均每请求 ${fmtCredit(t.credit_per_req)}`} />
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
          <dd>上游返回的实际扣费累计（非估算）。不同模型单价不同，故按模型分开看。</dd>
          <dt>统计范围</dt>
          <dd>
            网关是所有流量的必经点，因此这里<strong>包含其他客户端</strong>（脚本、第三方工具）的调用，
            不限于本面板发起的请求。统计持久化在网关的 data 目录，重启不丢。
          </dd>
          <dt>统计起点</dt>
          <dd>{stats ? fmtISO(stats.since) : '—'}</dd>
        </dl>
      </div>
    </>
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
