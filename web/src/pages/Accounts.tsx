// Accounts.tsx 账号管理：列表 / 筛选 / 单账号操作 / 批量任务 / 详情 / 导入导出。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { Account, AccountProfile, SessionInfo } from '../types'
import {
  Alert,
  Badge,
  ConfirmDialog,
  coolKindText,
  displayName,
  Empty,
  fmtDuration,
  fmtISO,
  fmtNum,
  fmtTime,
  Modal,
  Spinner,
  statusBadge,
} from '../ui'
import TaskProgress from './TaskProgress'

type Filter = 'all' | 'healthy' | 'cooling' | 'problem' | 'expiring'

export default function Accounts({ session }: { session: SessionInfo }) {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [fileIssues, setFileIssues] = useState<string[]>([])
  const [gatewayOK, setGatewayOK] = useState(true)
  const [gatewayError, setGatewayError] = useState<string | undefined>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'ok' | 'error' | 'warn'; text: string } | null>(null)

  const [filter, setFilter] = useState<Filter>('all')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [busyUid, setBusyUid] = useState<string | null>(null)

  const [detailUid, setDetailUid] = useState<string | null>(null)
  const [taskId, setTaskId] = useState<string | null>(null)
  const [showImport, setShowImport] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<Account | null>(null)
  const [deleting, setDeleting] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const res = await api.accounts()
      setAccounts(res.accounts ?? [])
      setFileIssues(res.file_issues ?? [])
      setGatewayOK(res.gateway_ok)
      setGatewayError(res.gateway_error)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '加载账号失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    const timer = setInterval(() => void load(true), 20_000)
    return () => clearInterval(timer)
  }, [load])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return accounts.filter((a) => {
      if (q && !a.uid.toLowerCase().includes(q) && !a.nickname.toLowerCase().includes(q)) return false
      switch (filter) {
        case 'healthy':
          return a.status === 'healthy'
        case 'cooling':
          return a.status === 'cooling'
        case 'problem':
          return a.status === 'disabled' || a.status === 'token_expired' || a.status === 'missing_credential'
        case 'expiring':
          return a.expired || a.needs_refresh
        default:
          return true
      }
    })
  }, [accounts, filter, query])

  const allSelected = filtered.length > 0 && filtered.every((a) => selected.has(a.uid))
  const targetUIDs = useMemo(
    () => (selected.size > 0 ? [...selected] : []),
    [selected],
  )

  const toggleAll = () => {
    if (allSelected) {
      setSelected(new Set())
    } else {
      setSelected(new Set(filtered.map((a) => a.uid)))
    }
  }

  const toggleOne = (uid: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(uid)) next.delete(uid)
      else next.add(uid)
      return next
    })
  }

  /** runSingle 执行单账号操作并展示结果。 */
  const runSingle = async (a: Account, action: 'checkin' | 'refresh' | 'travel' | 'credits') => {
    setBusyUid(a.uid)
    setNotice(null)
    try {
      if (action === 'checkin' || action === 'refresh' || action === 'travel') {
        const res =
          action === 'checkin'
            ? await api.accountCheckin(a.uid)
            : action === 'refresh'
              ? await api.accountRefresh(a.uid)
              : await api.accountTravel(a.uid)
        setNotice({
          kind: res.ok ? 'ok' : 'error',
          text: `${displayName(a)}：${res.message}${res.reward ? `（+${res.reward}）` : ''}`,
        })
      } else {
        const c = await api.accountCredits(a.uid)
        setNotice({
          kind: 'ok',
          text: `${displayName(a)}：剩余 ${fmtNum(c.remain)} / 总量 ${fmtNum(c.size)}（${c.packages} 个套餐）`,
        })
      }
      await load(true)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof ApiError ? err.message : '操作失败' })
    } finally {
      setBusyUid(null)
    }
  }

  /** runBatch 提交批量任务。 */
  const runBatch = async (kind: 'checkin' | 'refresh' | 'travel' | 'credits') => {
    setNotice(null)
    try {
      const fn =
        kind === 'checkin'
          ? api.batchCheckin
          : kind === 'refresh'
            ? api.batchRefresh
            : kind === 'travel'
              ? api.batchTravel
              : api.batchCredits
      const task = await fn(targetUIDs)
      setTaskId(task.id)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof ApiError ? err.message : '任务提交失败' })
    }
  }

  const doDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      const res = await api.deleteAccount(deleteTarget.uid)
      setNotice({ kind: 'ok', text: res.message })
      setDeleteTarget(null)
      await load(true)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof ApiError ? err.message : '删除失败' })
    } finally {
      setDeleting(false)
    }
  }

  const writeDisabled = session.read_only

  return (
    <>
      <div className="page-head">
        <div>
          <h1>账号管理</h1>
          <p>
            共 {accounts.length} 个账号
            {selected.size > 0 && <span className="text-accent"> · 已选 {selected.size} 个</span>}
          </p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={() => void load()} disabled={loading}>
            {loading ? <Spinner /> : '🔄'} 刷新
          </button>
          <button className="btn" onClick={() => setShowImport(true)} disabled={writeDisabled} title={writeDisabled ? '只读模式' : ''}>
            📥 导入凭证
          </button>
          <Link className="btn btn-primary" to="/login">
            ➕ 添加账号
          </Link>
        </div>
      </div>

      {writeDisabled && <Alert kind="warn">服务端已开启只读模式，签到 / 刷新 / 删除等写操作已禁用。</Alert>}
      {notice && (
        <Alert kind={notice.kind} onClose={() => setNotice(null)}>
          {notice.text}
        </Alert>
      )}
      {error && <Alert kind="error">{error}</Alert>}
      {!gatewayOK && (
        <Alert kind="warn">
          网关当前不可达{gatewayError ? `：${gatewayError}` : ''}。账号状态显示的是磁盘凭证信息，运行态为未知。
        </Alert>
      )}
      {fileIssues.length > 0 && (
        <Alert kind="warn">
          <strong>凭证目录存在无法解析的文件：</strong>
          <ul style={{ margin: '5px 0 0', paddingLeft: 18 }}>
            {fileIssues.map((i) => (
              <li key={i} className="mono" style={{ fontSize: 12 }}>
                {i}
              </li>
            ))}
          </ul>
        </Alert>
      )}

      {/* 批量操作区 */}
      <div className="card">
        <div className="card-head">
          <h2>批量操作</h2>
          <span className="hint">
            {selected.size > 0 ? `作用于已选 ${selected.size} 个账号` : '未选择时作用于全部账号'}
          </span>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={() => void runBatch('checkin')} disabled={writeDisabled || accounts.length === 0}>
            ✅ 批量签到
          </button>
          <button className="btn" onClick={() => void runBatch('refresh')} disabled={writeDisabled || accounts.length === 0}>
            🔑 批量刷新 Token
          </button>
          <button className="btn" onClick={() => void runBatch('travel')} disabled={writeDisabled || accounts.length === 0}>
            🐱 批量猫猫旅行
          </button>
          <button className="btn" onClick={() => void runBatch('credits')} disabled={accounts.length === 0}>
            💰 批量查积分
          </button>
          {selected.size > 0 && (
            <button className="btn btn-ghost" onClick={() => setSelected(new Set())}>
              清除选择
            </button>
          )}
        </div>
        <div className="field" style={{ marginTop: 13, marginBottom: 0 }}>
          <div className="desc">
            批量任务在后台串行执行（账号间限速，避免触发上游风控），可随时关闭进度窗口，任务会继续跑完。
          </div>
        </div>
      </div>

      {/* 账号列表 */}
      <div className="card">
        <div className="card-head">
          <h2>账号列表</h2>
          <div className="page-actions">
            <input
              type="text"
              placeholder="搜索 uid / 昵称…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              style={{ width: 190 }}
            />
            <select value={filter} onChange={(e) => setFilter(e.target.value as Filter)} style={{ width: 150 }}>
              <option value="all">全部</option>
              <option value="healthy">仅正常</option>
              <option value="cooling">仅冷却中</option>
              <option value="problem">仅异常</option>
              <option value="expiring">Token 需刷新</option>
            </select>
          </div>
        </div>

        {loading && accounts.length === 0 ? (
          <Spinner label="加载中…" />
        ) : filtered.length === 0 ? (
          <Empty>
            {accounts.length === 0 ? (
              <>
                还没有任何账号。
                <div style={{ marginTop: 10 }}>
                  <Link className="btn btn-primary btn-sm" to="/login">
                    ➕ 添加第一个账号
                  </Link>
                </div>
              </>
            ) : (
              '没有符合筛选条件的账号'
            )}
          </Empty>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th style={{ width: 34 }}>
                    <input type="checkbox" checked={allSelected} onChange={toggleAll} aria-label="全选" />
                  </th>
                  <th>账号</th>
                  <th>状态</th>
                  <th className="num">积分</th>
                  <th className="num">成功 / 失败</th>
                  <th>Token 有效期</th>
                  <th>最近活动</th>
                  <th style={{ minWidth: 210 }}>操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((a) => {
                  const b = statusBadge(a.status)
                  const busy = busyUid === a.uid
                  return (
                    <tr key={a.uid}>
                      <td>
                        <input
                          type="checkbox"
                          checked={selected.has(a.uid)}
                          onChange={() => toggleOne(a.uid)}
                          aria-label={`选择 ${a.uid}`}
                        />
                      </td>
                      <td>
                        <button
                          className="btn btn-ghost btn-sm"
                          style={{ padding: 0, color: 'var(--text)' }}
                          onClick={() => setDetailUid(a.uid)}
                          title="查看详情"
                        >
                          {displayName(a)}
                        </button>
                        <div className="mono text-faint" style={{ fontSize: 11 }}>
                          {a.uid.slice(0, 8)}
                          {a.in_gateway && a.in_flight > 0 && (
                            <span className="text-accent"> · 在途 {a.in_flight}</span>
                          )}
                        </div>
                      </td>
                      <td>
                        <Badge cls={b.cls}>{b.text}</Badge>
                        {a.cooling && (
                          <div className="text-faint" style={{ fontSize: 11.5, marginTop: 3 }}>
                            {coolKindText(a.cool_kind)} · 剩 {fmtDuration(a.cool_remaining_sec)}
                          </div>
                        )}
                        {a.disabled && a.reason && (
                          <div className="text-faint" style={{ fontSize: 11.5, marginTop: 3 }}>
                            {a.reason}
                          </div>
                        )}
                        {a.status === 'token_expired' && (
                          <div className="text-faint" style={{ fontSize: 11.5, marginTop: 3 }}>
                            需刷新或重新登录
                          </div>
                        )}
                      </td>
                      <td className="num">{fmtNum(a.live_credits ?? a.credits)}</td>
                      <td className="num text-dim" style={{ fontSize: 12 }}>
                        {fmtNum(a.success_count)} / {a.err_total > 0 ? <span className="text-warn">{a.err_total}</span> : '0'}
                      </td>
                      <td style={{ fontSize: 12 }}>
                        {a.expired ? (
                          <span className="text-danger">已过期</span>
                        ) : a.needs_refresh ? (
                          <span className="text-warn">即将过期</span>
                        ) : (
                          <span className="text-ok">有效</span>
                        )}
                        <div className="text-faint" style={{ fontSize: 11 }}>
                          {fmtTime(a.expires_at)}
                        </div>
                      </td>
                      <td style={{ fontSize: 12 }} className="text-dim">
                        {fmtISO(a.last_success || a.last_err)}
                      </td>
                      <td>
                        <div className="page-actions" style={{ gap: 5 }}>
                          <button
                            className="btn btn-sm"
                            disabled={busy || writeDisabled}
                            onClick={() => void runSingle(a, 'checkin')}
                            title="签到并刷新余额"
                          >
                            签到
                          </button>
                          <button
                            className="btn btn-sm"
                            disabled={busy || writeDisabled}
                            onClick={() => void runSingle(a, 'refresh')}
                            title="刷新 access token"
                          >
                            刷新
                          </button>
                          <button
                            className="btn btn-sm"
                            disabled={busy || writeDisabled}
                            onClick={() => void runSingle(a, 'travel')}
                            title="推进一趟猫猫旅行"
                          >
                            猫猫
                          </button>
                          <button className="btn btn-sm" disabled={busy} onClick={() => void runSingle(a, 'credits')} title="查询余额">
                            积分
                          </button>
                          <button
                            className="btn btn-sm btn-danger"
                            disabled={busy || session.read_only || !session.dangerous_ops || !a.has_file}
                            onClick={() => setDeleteTarget(a)}
                            title={
                              !a.has_file
                                ? '该账号在网关池中但没有本地凭证文件，无法删除'
                                : !session.dangerous_ops
                                  ? '需在服务端开启 dangerous_ops 才能删除账号'
                                  : session.read_only
                                    ? '只读模式'
                                    : '删除该账号凭证文件'
                            }
                          >
                            删除
                          </button>
                          {busy && <Spinner />}
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {detailUid && <AccountDetail uid={detailUid} onClose={() => setDetailUid(null)} />}

      {taskId && (
        <TaskProgress
          taskId={taskId}
          onClose={() => setTaskId(null)}
          onFinished={() => void load(true)}
        />
      )}

      {showImport && (
        <ImportDialog
          onClose={() => setShowImport(false)}
          onDone={(msg) => {
            setShowImport(false)
            setNotice({ kind: 'ok', text: msg })
            void load(true)
          }}
          onError={(msg) => setNotice({ kind: 'error', text: msg })}
        />
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="删除账号凭证"
          danger
          confirmText="确认删除"
          busy={deleting}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void doDelete()}
          message={
            <>
              <p style={{ marginTop: 0 }}>
                即将删除账号 <strong>{displayName(deleteTarget)}</strong>
                <span className="mono text-faint"> ({deleteTarget.uid.slice(0, 8)})</span> 的凭证文件
                <span className="mono"> {deleteTarget.file_name}</span>。
              </p>
              <p>
                该操作<strong className="text-danger">不可从上游恢复</strong>：删除后需要重新走 OAuth 登录才能找回该账号。
                网关需重启后才会把它移出账号池。
              </p>
            </>
          }
        />
      )}
    </>
  )
}

/** AccountDetail 账号详情弹窗：实时积分、猫档案、冷却/熔断细节。 */
function AccountDetail({ uid, onClose }: { uid: string; onClose: () => void }) {
  const [profile, setProfile] = useState<AccountProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    api
      .account(uid)
      .then((p) => {
        if (!cancelled) setProfile(p)
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof ApiError ? err.message : '加载详情失败')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [uid])

  const a = profile?.account

  return (
    <Modal title={`账号详情 · ${a ? displayName(a) : uid.slice(0, 8)}`} onClose={onClose} wide>
      {loading && <Spinner label="正在查询上游…" />}
      {error && <Alert kind="error">{error}</Alert>}
      {profile?.upstream_error && <Alert kind="warn">上游查询失败：{profile.upstream_error}</Alert>}

      {a && (
        <>
          <div className="grid grid-stats" style={{ marginBottom: 16 }}>
            <div className="stat">
              <div className="stat-label">剩余积分</div>
              <div className="stat-value small">
                {profile?.credits ? fmtNum(profile.credits.remain) : fmtNum(a.live_credits ?? a.credits)}
              </div>
              {profile?.credits && <div className="stat-sub">总量 {fmtNum(profile.credits.size)}</div>}
              {profile?.credits_error && <div className="stat-sub text-danger">查询失败</div>}
            </div>
            <div className="stat">
              <div className="stat-label">累计成功</div>
              <div className="stat-value small text-ok">{fmtNum(a.success_count)}</div>
              <div className="stat-sub">失败 {fmtNum(a.err_total)}</div>
            </div>
            <div className="stat">
              <div className="stat-label">在途请求</div>
              <div className="stat-value small">{a.in_flight}</div>
              <div className="stat-sub">熔断计数 {a.breaker_fails}</div>
            </div>
            <div className="stat">
              <div className="stat-label">猫猫旅行</div>
              <div className="stat-value small" style={{ fontSize: 15 }}>
                {profile?.buddy ? profile.buddy.name || '已领养' : profile?.buddy_error ? '查询失败' : '未领养'}
              </div>
              <div className="stat-sub">
                {profile?.travel
                  ? `状态 ${profile.travel.state}${profile.travel.daily_limit_reached ? '（今日已派出）' : ''}`
                  : '—'}
              </div>
            </div>
          </div>

          <dl className="kv">
            <dt>UID</dt>
            <dd className="mono">{a.uid}</dd>
            <dt>昵称</dt>
            <dd>{a.nickname || '—'}</dd>
            <dt>企业 ID</dt>
            <dd className="mono">{a.enterprise_id || '—'}</dd>
            <dt>Domain</dt>
            <dd className="mono">{a.domain || '—'}</dd>
            <dt>凭证文件</dt>
            <dd className="mono">{a.file_name || '（无文件，仅网关内存）'}</dd>
            <dt>Token 过期</dt>
            <dd>{fmtTime(a.expires_at)}</dd>
            <dt>Refresh Token</dt>
            <dd>{a.has_refresh_token ? '✅ 存在' : '❌ 缺失（无法刷新，需重登）'}</dd>
            <dt>网关状态</dt>
            <dd>
              <Badge cls={statusBadge(a.status).cls}>{statusBadge(a.status).text}</Badge>
              {a.reason && <span className="text-faint"> · {a.reason}</span>}
            </dd>
            {a.cooling && (
              <>
                <dt>冷却类型</dt>
                <dd>
                  {coolKindText(a.cool_kind)} · 剩余 {fmtDuration(a.cool_remaining_sec)}
                  {a.soft_streak ? ` · 连续软限流 ${a.soft_streak} 次` : ''}
                </dd>
                <dt>冷却截止</dt>
                <dd>{fmtISO(a.breaker_until) !== '—' ? fmtISO(a.breaker_until) : fmtTime(a.expires_at)}</dd>
              </>
            )}
            <dt>最近成功</dt>
            <dd>{fmtISO(a.last_success)}</dd>
            <dt>最近失败</dt>
            <dd>{fmtISO(a.last_err)}</dd>
          </dl>
        </>
      )}

      <div className="modal-foot">
        <button className="btn" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}

/** ImportDialog 手工导入凭证（从别的机器迁移 / 复用 login.sh 产物）。 */
function ImportDialog({
  onClose,
  onDone,
  onError,
}: {
  onClose: () => void
  onDone: (msg: string) => void
  onError: (msg: string) => void
}) {
  const [raw, setRaw] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    setError(null)
    setBusy(true)
    try {
      const res = await api.importAccount({ raw_json: raw })
      if (res.ok) onDone(res.message)
      else setError(res.message)
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : '导入失败'
      setError(msg)
      onError(msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title="导入账号凭证"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn btn-primary" onClick={() => void submit()} disabled={busy || !raw.trim()}>
            {busy ? <Spinner /> : null}
            导入
          </button>
        </>
      }
    >
      {error && <Alert kind="error">{error}</Alert>}
      <div className="field">
        <label>粘贴凭证 JSON</label>
        <textarea
          rows={12}
          value={raw}
          onChange={(e) => setRaw(e.target.value)}
          placeholder={`支持两种格式：

1) workbuddy2api / 插件的嵌套格式（推荐，直接 cat auths/workbuddy-*.json 的内容）：
{
  "account": { "uid": "...", "enterpriseId": "", "nickname": "..." },
  "auth": { "accessToken": "...", "refreshToken": "...", "expiresAt": 1794289203, "domain": "copilot.tencent.com" }
}

2) 扁平格式：
{ "accessToken": "...", "refreshToken": "...", "uid": "...", "nickname": "..." }`}
        />
      </div>
      <div className="desc">
        导入后需重启网关容器，账号才会进入账号池（可在「系统」页一键重启）。若无 refreshToken，该账号在 token
        过期后必须重新登录。
      </div>
    </Modal>
  )
}
