// ConfigPage.tsx 网关 config.json 在线编辑：表单模式 + JSON 源码模式。
//
// 关键设计：表单读写的是**完整原始文档对象**（只改动已知字段），
// 因此配置文件里 GUi 不认识的自定义字段会被原样保留，不会因为一次保存被抹掉。
import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { ConfigMeta, SessionInfo } from '../types'
import { Alert, ConfirmDialog, fmtISO, Spinner } from '../ui'

type Doc = Record<string, unknown>

/** 按路径读写嵌套字段。 */
function getPath(doc: Doc, path: string): unknown {
  return path.split('.').reduce<unknown>((acc, key) => {
    if (acc && typeof acc === 'object') return (acc as Doc)[key]
    return undefined
  }, doc)
}

/** 设置嵌套字段（不可变更新，保证 React 看到新对象）。 */
function setPath(doc: Doc, path: string, value: unknown): Doc {
  const keys = path.split('.')
  const clone: Doc = { ...doc }
  let cur: Doc = clone
  for (let i = 0; i < keys.length - 1; i++) {
    const k = keys[i]
    const next = cur[k]
    cur[k] = next && typeof next === 'object' ? { ...(next as Doc) } : {}
    cur = cur[k] as Doc
  }
  cur[keys[keys.length - 1]] = value
  return clone
}

/** 数字字段：空串表示"未设置"，返回 undefined 以便从配置中删除。 */
function numOrUndefined(v: string): number | undefined {
  if (v.trim() === '') return undefined
  const n = Number(v)
  return Number.isFinite(n) ? n : undefined
}

/** 小时数组的文本互转：逗号/空格分隔。 */
function hoursToText(v: unknown): string {
  if (Array.isArray(v)) return v.join(', ')
  return ''
}

function textToHours(text: string): number[] | undefined {
  const parts = text
    .split(/[,，\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  if (parts.length === 0) return undefined
  const nums = parts.map((s) => parseInt(s, 10))
  if (nums.some((n) => Number.isNaN(n))) return undefined
  return nums
}

export default function ConfigPage({ session }: { session: SessionInfo }) {
  const [doc, setDoc] = useState<Doc | null>(null)
  const [meta, setMeta] = useState<ConfigMeta | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [tab, setTab] = useState<'form' | 'json'>('form')
  const [jsonText, setJsonText] = useState('')
  const [jsonError, setJsonError] = useState<string | null>(null)
  const [showApiKey, setShowApiKey] = useState(false)
  const [confirmReset, setConfirmReset] = useState(false)
  const [resetting, setResetting] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await api.config()
      setDoc(res.config)
      setMeta(res.meta)
      setJsonText(JSON.stringify(res.config, null, 2))
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '加载配置失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  /** 表单字段更新：同时同步 JSON 文本，保证两个视图一致。 */
  const update = (path: string, value: unknown) => {
    setDoc((prev) => {
      if (!prev) return prev
      const next = setPath(prev, path, value)
      setJsonText(JSON.stringify(next, null, 2))
      return next
    })
  }

  const saveForm = async () => {
    if (!doc) return
    setSaving(true)
    setNotice(null)
    try {
      const res = await api.saveConfig(doc)
      setNotice(res.message)
      setError(null)
      // 保存后重新读取：首次保存会生成备份文件，meta 里的 backup_path 随之出现
      //（「恢复备份」按钮依赖它）；顺带同步文件修改时间。
      await reloadMeta()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  /** reloadMeta 只刷新元信息与已保存内容，用于保存成功后同步状态。 */
  const reloadMeta = async () => {
    try {
      const res = await api.config()
      setMeta(res.meta)
      // 用服务端返回的规范化内容覆盖本地（例如 JSON 缩进、字段顺序）。
      setDoc(res.config)
      setJsonText(JSON.stringify(res.config, null, 2))
    } catch {
      // 刷新失败不影响"已保存"这一事实，保留当前视图。
    }
  }

  const saveJSON = async () => {
    setJsonError(null)
    let parsed: Doc
    try {
      parsed = JSON.parse(jsonText) as Doc
    } catch (err) {
      setJsonError(err instanceof Error ? err.message : 'JSON 语法错误')
      return
    }
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      setJsonError('配置顶层必须是一个 JSON 对象')
      return
    }
    setSaving(true)
    setNotice(null)
    try {
      const res = await api.saveConfig(parsed)
      setNotice(res.message)
      setError(null)
      await reloadMeta()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const doReset = async () => {
    setResetting(true)
    try {
      const res = await api.resetConfig()
      setConfirmReset(false)
      setNotice(res.message)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '恢复失败')
    } finally {
      setResetting(false)
    }
  }

  if (loading && !doc) return <Spinner label="正在读取网关配置…" />

  const writeDisabled = session.read_only
  const str = (path: string) => {
    const v = getPath(doc ?? {}, path)
    return v === undefined || v === null ? '' : String(v)
  }
  const bool = (path: string) => getPath(doc ?? {}, path) === true
  const numStr = (path: string) => {
    const v = getPath(doc ?? {}, path)
    return v === undefined || v === null ? '' : String(v)
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>网关配置</h1>
          <p>
            在线编辑 <span className="mono">{meta?.path || 'config.json'}</span>
            {meta?.mod_time && <span className="text-faint"> · 最后修改 {fmtISO(meta.mod_time)}</span>}
          </p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={() => void load()} disabled={loading}>
            {loading ? <Spinner /> : '🔄'} 重新读取
          </button>
          {meta?.backup_path && (
            <button
              className="btn btn-danger"
              onClick={() => setConfirmReset(true)}
              disabled={writeDisabled || !session.dangerous_ops}
              title={!session.dangerous_ops ? '需在服务端开启 dangerous_ops' : '从首次备份恢复'}
            >
              ↩️ 恢复备份
            </button>
          )}
        </div>
      </div>

      {writeDisabled && <Alert kind="warn">服务端已开启只读模式，配置无法保存。</Alert>}
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
      {meta && !meta.exists && (
        <Alert kind="warn">
          配置文件尚不存在（{meta.path}）。保存后会以当前内容创建；网关首次启动时用默认值。
        </Alert>
      )}

      <Alert kind="info">
        <strong>修改后需要重启网关才生效。</strong> 保存只会写文件，不会自动重启。
        {session.dangerous_ops ? (
          <>
            {' '}可在「系统」页一键重启容器。
          </>
        ) : (
          <>
            {' '}（自动重启能力需在服务端开启 <span className="mono">dangerous_ops</span>）
          </>
        )}
        {meta?.backup_path ? (
          <div style={{ marginTop: 5, fontSize: 12.5 }}>
            首次保存前已自动备份原始文件到 <span className="mono">{meta.backup_path}</span>
            {meta.backup_at ? `（${fmtISO(meta.backup_at)}）` : ''}。随时可用右上角「恢复备份」回滚。
          </div>
        ) : (
          <div style={{ marginTop: 5, fontSize: 12.5 }}>
            尚未生成备份：首次保存时会自动把当前的 config.json 备份一份，之后可随时回滚。
          </div>
        )}
      </Alert>

      <div className="tabs">
        <button className={`tab ${tab === 'form' ? 'active' : ''}`} onClick={() => setTab('form')}>
          表单编辑
        </button>
        <button className={`tab ${tab === 'json' ? 'active' : ''}`} onClick={() => setTab('json')}>
          JSON 源码
        </button>
      </div>

      {tab === 'form' && doc && (
        <>
          <div className="card">
            <div className="card-head">
              <h2>基础设置</h2>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>监听地址 listen</label>
                <input type="text" value={str('listen')} onChange={(e) => update('listen', e.target.value)} placeholder=":7863" />
                <div className="desc">网关 HTTP 监听地址，冒号开头表示所有网卡。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>API 密钥 api_key</label>
                <div style={{ display: 'flex', gap: 6 }}>
                  <input
                    type={showApiKey ? 'text' : 'password'}
                    value={str('api_key')}
                    onChange={(e) => update('api_key', e.target.value)}
                    placeholder="留空 = 不鉴权（公网务必设置）"
                  />
                  <button className="btn shrink" onClick={() => setShowApiKey((v) => !v)} type="button">
                    {showApiKey ? '隐藏' : '显示'}
                  </button>
                </div>
                <div className="desc">
                  客户端调用 <span className="mono">/v1/chat/completions</span> 时需带
                  <span className="mono"> Authorization: Bearer &lt;api_key&gt;</span>。留空则任何人可用。
                </div>
              </div>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>凭证目录 auth_dir</label>
                <input type="text" value={str('auth_dir')} onChange={(e) => update('auth_dir', e.target.value)} placeholder="./auths" />
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>状态文件 state_file</label>
                <input type="text" value={str('state_file')} onChange={(e) => update('state_file', e.target.value)} placeholder="./data/state.json" />
              </div>
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <h2>定时任务</h2>
              <span className="hint">时间按容器时区（compose 默认 Asia/Shanghai）</span>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>签到小时 checkin_hours</label>
                <input
                  type="text"
                  value={hoursToText(getPath(doc, 'schedule.checkin_hours'))}
                  onChange={(e) => update('schedule.checkin_hours', textToHours(e.target.value))}
                  placeholder="9, 21"
                />
                <div className="desc">逗号分隔的整点（0-23）。签到同时会推进猫猫旅行。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>保活小时 keepalive_hours</label>
                <input
                  type="text"
                  value={hoursToText(getPath(doc, 'schedule.keepalive_hours'))}
                  onChange={(e) => update('schedule.keepalive_hours', textToHours(e.target.value))}
                  placeholder="22"
                />
                <div className="desc">在该整点刷新全部账号的 token。</div>
              </div>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label className="checkbox">
                  <input
                    type="checkbox"
                    checked={bool('schedule.checkin_enabled')}
                    onChange={(e) => update('schedule.checkin_enabled', e.target.checked)}
                  />
                  启用签到
                </label>
                <div className="desc">关闭后签到与猫猫旅行都会停摆（旅行搭签到便车）。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label className="checkbox">
                  <input
                    type="checkbox"
                    checked={bool('schedule.keepalive_enabled')}
                    onChange={(e) => update('schedule.keepalive_enabled', e.target.checked)}
                  />
                  启用 Token 保活
                </label>
              </div>
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <h2>账号池与冷却</h2>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>软限流冷却基数 cooldown.soft_rate</label>
                <input type="text" value={str('cooldown.soft_rate')} onChange={(e) => update('cooldown.soft_rate', e.target.value)} placeholder="600s" />
                <div className="desc">429/限流文案触发的冷却时长，连续触发按 2 倍指数退避。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>软冷却退避封顶 cooldown.soft_rate_max</label>
                <input type="text" value={str('cooldown.soft_rate_max')} onChange={(e) => update('cooldown.soft_rate_max', e.target.value)} placeholder="2h" />
              </div>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>单账号最大在途 pool.max_in_flight</label>
                <input
                  type="text"
                  value={numStr('pool.max_in_flight')}
                  onChange={(e) => update('pool.max_in_flight', numOrUndefined(e.target.value))}
                  placeholder="3"
                />
                <div className="desc">0 = 不限制并发。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>熔断阈值 pool.breaker_threshold</label>
                <input
                  type="text"
                  value={numStr('pool.breaker_threshold')}
                  onChange={(e) => update('pool.breaker_threshold', numOrUndefined(e.target.value))}
                  placeholder="3"
                />
                <div className="desc">连续失败达到该次数即熔断。</div>
              </div>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>熔断基础时长 pool.breaker_cooldown</label>
                <input type="text" value={str('pool.breaker_cooldown')} onChange={(e) => update('pool.breaker_cooldown', e.target.value)} placeholder="30m" />
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>熔断退避封顶 pool.breaker_cooldown_max</label>
                <input type="text" value={str('pool.breaker_cooldown_max')} onChange={(e) => update('pool.breaker_cooldown_max', e.target.value)} placeholder="6h" />
              </div>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>闲置补偿速率 pool.idle_weight_per_hour</label>
                <input
                  type="text"
                  value={numStr('pool.idle_weight_per_hour')}
                  onChange={(e) => update('pool.idle_weight_per_hour', numOrUndefined(e.target.value))}
                  placeholder="0.5"
                />
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>闲置补偿封顶 pool.idle_weight_max</label>
                <input
                  type="text"
                  value={numStr('pool.idle_weight_max')}
                  onChange={(e) => update('pool.idle_weight_max', numOrUndefined(e.target.value))}
                  placeholder="5.0"
                />
              </div>
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <h2>上游超时</h2>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label>短 RPC 总超时（秒）</label>
                <input
                  type="text"
                  value={numStr('upstream.timeout_seconds')}
                  onChange={(e) => update('upstream.timeout_seconds', numOrUndefined(e.target.value))}
                  placeholder="120"
                />
                <div className="desc">刷新 token / 签到 / 余额 / 模型列表的硬上限。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>聊天首字节超时（秒）</label>
                <input
                  type="text"
                  value={numStr('upstream.header_timeout_seconds')}
                  onChange={(e) => update('upstream.header_timeout_seconds', numOrUndefined(e.target.value))}
                  placeholder="120"
                />
                <div className="desc">超时即换号重发；留空回落 timeout_seconds。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>聊天流空闲超时（秒）</label>
                <input
                  type="text"
                  value={numStr('upstream.idle_timeout_seconds')}
                  onChange={(e) => update('upstream.idle_timeout_seconds', numOrUndefined(e.target.value))}
                  placeholder="300"
                />
                <div className="desc">持续吐数据不受影响，静默超时才断流。</div>
              </div>
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <h2>会话粘性与功能开关</h2>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 1 }}>
                <label className="checkbox">
                  <input
                    type="checkbox"
                    checked={bool('session_sticky.enabled')}
                    onChange={(e) => update('session_sticky.enabled', e.target.checked)}
                  />
                  启用会话粘性
                </label>
                <div className="desc">同一会话固定到同一账号，多轮对话不跳号。</div>
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>粘性 TTL</label>
                <input type="text" value={str('session_sticky.ttl')} onChange={(e) => update('session_sticky.ttl', e.target.value)} placeholder="30m" />
              </div>
              <div className="field" style={{ flex: 1 }}>
                <label>粘性 GC 周期</label>
                <input
                  type="text"
                  value={str('session_sticky.gc_interval')}
                  onChange={(e) => update('session_sticky.gc_interval', e.target.value)}
                  placeholder="5m"
                />
              </div>
            </div>
            <div className="field" style={{ marginBottom: 0 }}>
              <label className="checkbox">
                <input
                  type="checkbox"
                  checked={bool('features.sanitize_blacklist_fingerprints')}
                  onChange={(e) => update('features.sanitize_blacklist_fingerprints', e.target.checked)}
                />
                出站请求体指纹脱敏
              </label>
              <div className="desc">清洗请求体中的黑名单指纹字段，降低被上游识别为自动化工具的风险。</div>
            </div>
          </div>

          <div className="card">
            <div className="card-head">
              <h2>Redis 镜像（可选）</h2>
              <span className="hint">留空 = 纯内存模式，功能照常</span>
            </div>
            <div className="row">
              <div className="field" style={{ flex: 2 }}>
                <label>Upstash URL</label>
                <input type="text" value={str('upstash.url')} onChange={(e) => update('upstash.url', e.target.value)} placeholder="rediss://… 或 https://xxx.upstash.io" />
              </div>
              <div className="field" style={{ flex: 2 }}>
                <label>Upstash Token</label>
                <input
                  type={showApiKey ? 'text' : 'password'}
                  value={str('upstash.token')}
                  onChange={(e) => update('upstash.token', e.target.value)}
                  placeholder="REST token"
                />
              </div>
            </div>
          </div>

          <div className="page-actions">
            <button className="btn btn-primary" onClick={() => void saveForm()} disabled={saving || writeDisabled}>
              {saving ? <Spinner /> : '💾'} 保存配置
            </button>
            <button className="btn" onClick={() => void load()} disabled={saving}>
              放弃修改
            </button>
          </div>
        </>
      )}

      {tab === 'json' && (
        <div className="card">
          <div className="card-head">
            <h2>JSON 源码</h2>
            <span className="hint">适合编辑表单未覆盖的自定义字段</span>
          </div>
          {jsonError && <Alert kind="error">JSON 语法错误：{jsonError}</Alert>}
          <textarea
            rows={26}
            value={jsonText}
            onChange={(e) => {
              setJsonText(e.target.value)
              setJsonError(null)
            }}
            spellCheck={false}
            style={{ minHeight: 460 }}
          />
          <div className="page-actions" style={{ marginTop: 13 }}>
            <button className="btn btn-primary" onClick={() => void saveJSON()} disabled={saving || writeDisabled}>
              {saving ? <Spinner /> : '💾'} 保存配置
            </button>
            <button className="btn" onClick={() => setJsonText(JSON.stringify(doc ?? {}, null, 2))}>
              还原为已加载内容
            </button>
          </div>
        </div>
      )}

      {confirmReset && (
        <ConfirmDialog
          title="从备份恢复配置"
          danger
          confirmText="确认恢复"
          busy={resetting}
          onCancel={() => setConfirmReset(false)}
          onConfirm={() => void doReset()}
          message={
            <>
              将用首次保存前的备份文件覆盖当前 <span className="mono">{meta?.path}</span>。
              <br />
              备份路径：<span className="mono">{meta?.backup_path}</span>
            </>
          }
        />
      )}
    </>
  )
}
