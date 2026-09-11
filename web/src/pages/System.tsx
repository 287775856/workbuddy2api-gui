// System.tsx 系统页：运行信息、容器状态与控制、任务历史、使用说明。
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { SessionInfo, SystemInfo, TaskListResponse } from '../types'
import { Alert, Badge, ConfirmDialog, fmtDuration, fmtISO, Spinner } from '../ui'

export default function System({
  session,
  onSessionRefresh,
}: {
  session: SessionInfo
  onSessionRefresh: () => Promise<SessionInfo | null>
}) {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [tasks, setTasks] = useState<TaskListResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [confirmRestart, setConfirmRestart] = useState(false)
  const [restarting, setRestarting] = useState(false)

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const [i, t] = await Promise.all([api.system(), api.tasks()])
      setInfo(i)
      setTasks(t)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '加载系统信息失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    const timer = setInterval(() => void load(true), 20_000)
    return () => clearInterval(timer)
  }, [load])

  const doRestart = async () => {
    setRestarting(true)
    setNotice(null)
    try {
      const res = await api.restart()
      setNotice(res.message + '。网关需数秒恢复，请稍后刷新页面查看账号加载情况。')
      setConfirmRestart(false)
      // 重启不改变面板自身配置，但同步一次会话信息以保证模式标志（只读/高危）最新。
      void onSessionRefresh()
      // 给容器一点启动时间再刷新状态。
      setTimeout(() => void load(true), 4000)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '重启失败')
    } finally {
      setRestarting(false)
    }
  }

  if (loading && !info) return <Spinner label="正在加载系统信息…" />

  const c = info?.container
  const gatewayServiceOK = info?.gateway_health?.service === 'workbuddy2api'

  return (
    <>
      <div className="page-head">
        <div>
          <h1>系统</h1>
          <p>面板与网关的运行状态、容器控制与任务历史</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={() => void load()} disabled={loading}>
            {loading ? <Spinner /> : '🔄'} 刷新
          </button>
          <button
            className="btn btn-primary"
            onClick={() => setConfirmRestart(true)}
            disabled={!session.dangerous_ops || !c?.exists}
            title={
              !session.dangerous_ops
                ? '需在服务端开启 dangerous_ops 才能重启容器'
                : !c?.exists
                  ? '未检测到容器'
                  : '重启网关容器以加载新账号/新配置'
            }
          >
            🔁 重启网关
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
      {session.using_default_password && (
        <Alert kind="warn">
          面板正在使用默认口令，请在服务端配置中修改 <span className="mono">ui.password</span>。
        </Alert>
      )}

      {/* 网关与容器状态 */}
      <div className="card">
        <div className="card-head">
          <h2>网关状态</h2>
          {gatewayServiceOK ? <Badge cls="badge-ok">运行中</Badge> : <Badge cls="badge-danger">不可达</Badge>}
        </div>
        <dl className="kv">
          <dt>网关地址</dt>
          <dd className="mono">{info?.gateway_url}</dd>
          <dt>身份标识</dt>
          <dd>
            {gatewayServiceOK ? (
              <>service = <span className="mono">workbuddy2api</span> ✅</>
            ) : (
              <span className="text-danger">{info?.gateway_health_error || '无法确认身份'}</span>
            )}
          </dd>
          <dt>账号池</dt>
          <dd>
            {info?.gateway_health ? `${info.gateway_health.healthy} / ${info.gateway_health.total} 可用` : '—'}
          </dd>
          <dt>容器名</dt>
          <dd className="mono">{c?.name || '（未配置）'}</dd>
          <dt>容器状态</dt>
          <dd>
            {c?.disabled ? (
              <span className="text-faint">未配置 docker_container</span>
            ) : c?.exists ? (
              <>
                <Badge cls={c.running ? 'badge-ok' : 'badge-danger'}>{c.status || 'unknown'}</Badge>
                {c.health && c.health !== '-' && (
                  <span className="text-faint" style={{ marginLeft: 8 }}>
                    健康检查：{c.health}
                  </span>
                )}
              </>
            ) : (
              <span className="text-danger">{c?.error || '容器不存在'}</span>
            )}
          </dd>
          <dt>容器镜像</dt>
          <dd className="mono">{c?.image || '—'}</dd>
          <dt>容器启动</dt>
          <dd>{c?.started_at ? fmtISO(c.started_at) : '—'}</dd>
        </dl>
        {!session.dangerous_ops && (
          <div className="desc" style={{ marginTop: 12 }}>
            当前未开启高危操作（<span className="mono">dangerous_ops=false</span>），因此「重启网关」「删除账号」
            「恢复配置备份」均被禁用。如需启用，在面板服务端配置中设置：
            <div className="muted-box" style={{ marginTop: 7 }}>{`WBGUI_DANGEROUS_OPS=true  # 或配置文件里 "dangerous_ops": true`}</div>
          </div>
        )}
      </div>

      {/* 面板信息 */}
      <div className="card">
        <div className="card-head">
          <h2>面板信息</h2>
        </div>
        <dl className="kv">
          <dt>面板版本</dt>
          <dd className="mono">{info?.version || 'dev'}</dd>
          <dt>运行时长</dt>
          <dd>{info ? fmtDuration(info.uptime_sec) : '—'}</dd>
          <dt>启动时间</dt>
          <dd>{info ? fmtISO(info.started_at) : '—'}</dd>
          <dt>凭证目录</dt>
          <dd className="mono">{info?.auth_dir}</dd>
          <dt>网关配置文件</dt>
          <dd className="mono">{info?.config_file}</dd>
          <dt>运行模式</dt>
          <dd>
            {info?.read_only ? <Badge cls="badge-warn">只读</Badge> : <Badge cls="badge-ok">可写</Badge>}
            {info?.dangerous_ops ? (
              <Badge cls="badge-warn">高危操作已解锁</Badge>
            ) : (
              <Badge cls="badge-dim">高危操作已锁定</Badge>
            )}
          </dd>
          <dt>Docker 可用</dt>
          <dd>{info?.docker_available ? '✅ 是' : '❌ 否（容器控制已降级）'}</dd>
        </dl>
      </div>

      {/* 任务历史 */}
      <div className="card">
        <div className="card-head">
          <h2>任务历史</h2>
          <span className="hint">保留最近 20 条（进程重启后清空）</span>
        </div>
        {tasks?.running && tasks.running.length > 0 && (
          <Alert kind="info">
            当前有 {tasks.running.length} 个任务正在执行：{tasks.running.map((t) => t.title).join('、')}
          </Alert>
        )}
        {!tasks?.tasks || tasks.tasks.length === 0 ? (
          <div className="empty">
            还没有执行过批量任务。
            <div style={{ marginTop: 10 }}>
              <Link className="btn btn-sm" to="/accounts">
                去账号管理执行
              </Link>
            </div>
          </div>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>任务</th>
                  <th>状态</th>
                  <th className="num">成功 / 失败</th>
                  <th>开始时间</th>
                </tr>
              </thead>
              <tbody>
                {tasks.tasks.map((t) => (
                  <tr key={t.id}>
                    <td>{t.title}</td>
                    <td>
                      {t.running ? (
                        <Badge cls="badge-accent">执行中</Badge>
                      ) : t.failed > 0 ? (
                        <Badge cls="badge-warn">已完成（有失败）</Badge>
                      ) : (
                        <Badge cls="badge-ok">已完成</Badge>
                      )}
                    </td>
                    <td className="num">
                      <span className="text-ok">{t.ok}</span> /{' '}
                      {t.failed > 0 ? <span className="text-danger">{t.failed}</span> : <span className="text-dim">0</span>}
                    </td>
                    <td className="text-dim" style={{ fontSize: 12 }}>
                      {fmtISO(t.started_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 使用说明 */}
      <div className="card">
        <div className="card-head">
          <h2>客户端接入方式</h2>
        </div>
        <p className="text-dim" style={{ marginTop: 0, fontSize: 13 }}>
          网关对客户端暴露标准 OpenAI 兼容接口，任何支持自定义 base_url 的客户端都可直接接入：
        </p>
        <div className="muted-box">
          {`Base URL:  ${info?.gateway_url || 'http://127.0.0.1:7863'}/v1
API Key:   你在网关 config.json 中设置的 api_key
模型:      见「聊天测试」页的模型列表（如 deepseek-v4.1-flash、glm-5.3 等）`}
        </div>
        <div className="muted-box" style={{ marginTop: 10 }}>
          {`# 命令行验证
curl ${info?.gateway_url || 'http://127.0.0.1:7863'}/v1/chat/completions \\
  -H "Authorization: Bearer <你的 api_key>" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}],"stream":true}'`}
        </div>
        <div className="desc" style={{ marginTop: 10 }}>
          提示：本面板与网关是<strong>两个独立服务</strong>。面板只通过 HTTP API 管理网关，并直读磁盘上的账号凭证与
          配置文件；因此网关重启不会影响面板，反之亦然。
        </div>
      </div>

      {confirmRestart && (
        <ConfirmDialog
          title="重启网关容器"
          danger
          confirmText="确认重启"
          busy={restarting}
          onCancel={() => setConfirmRestart(false)}
          onConfirm={() => void doRestart()}
          message={
            <>
              <p style={{ marginTop: 0 }}>
                即将执行 <span className="mono">docker restart {c?.name}</span>。
              </p>
              <p>
                重启期间（约 2-5 秒）网关无法处理请求，正在进行的对话会中断。
                重启后新账号会被加载进账号池、新配置会生效。
              </p>
              <p style={{ marginBottom: 0 }}>账号池状态由 state.json 持久化，重启不会丢失冷却/熔断记录。</p>
            </>
          }
        />
      )}
    </>
  )
}
