// LoginWizard.tsx 网页版 OAuth 设备授权登录：替代 login.sh 的全流程。
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { LoginSession, SessionInfo } from '../types'
import { Alert, Spinner } from '../ui'

const STEPS = ['发起授权', '浏览器登录', '保存凭证'] as const

export default function LoginWizard({
  session,
  onDone,
}: {
  session: SessionInfo
  onDone: () => Promise<SessionInfo | null>
}) {
  const [sess, setSess] = useState<LoginSession | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const pollRef = useRef<number | null>(null)
  const [manualUID, setManualUID] = useState<string | null>(null)
  const [region, setRegion] = useState<'cn' | 'global'>('cn')

  // 清理轮询定时器，避免离开页面后继续打接口。
  useEffect(() => {
    return () => {
      if (pollRef.current) window.clearInterval(pollRef.current)
    }
  }, [])

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      window.clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  const start = async () => {
    setError(null)
    setBusy(true)
    stopPolling()
    try {
      const s = await api.loginStart(region)
      setSess(s)
      // 自动开始轮询：用户完成浏览器登录后无需手动点。
      pollRef.current = window.setInterval(() => void poll(s.id), 2500)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '发起登录失败')
    } finally {
      setBusy(false)
    }
  }

  const poll = useCallback(
    async (id: string) => {
      try {
        const s = await api.loginPoll(id)
        setSess(s)
        if (s.status !== 'pending') {
          stopPolling()
          if (s.status === 'success') {
            // 登录成功后刷新账号列表所需的会话信息（口令未变，仅为了统一入口）。
            void onDone()
          }
        }
      } catch (err) {
        // 单次轮询失败不终止：网络抖动时下一轮会自愈；连续失败由用户手动重试。
        setError(err instanceof ApiError ? err.message : '轮询失败')
      }
    },
    [stopPolling, onDone],
  )

  const cancel = async () => {
    if (!sess) return
    stopPolling()
    try {
      setSess(await api.loginCancel(sess.id))
    } catch {
      setError('取消失败')
    }
  }

  const copyURL = async () => {
    if (!sess) return
    try {
      await navigator.clipboard.writeText(sess.auth_url)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // 剪贴板不可用（非 HTTPS / 无权限）时提示用户手动复制。
      setError('浏览器拒绝了剪贴板访问，请手动选中链接复制')
    }
  }

  const stepIndex = !sess ? 0 : sess.status === 'success' ? 3 : 1
  const writeDisabled = session.read_only

  return (
    <>
      <div className="page-head">
        <div>
          <h1>添加账号</h1>
          <p>通过 WorkBuddy OAuth 设备授权登录（支持国内版 / 国际版），把账号加入账号池</p>
        </div>
        <div className="page-actions">
          <Link className="btn" to="/accounts">
            ← 返回账号管理
          </Link>
        </div>
      </div>

      {writeDisabled && <Alert kind="warn">服务端已开启只读模式，无法添加账号。</Alert>}
      {error && (
        <Alert kind="error" onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      {/* 步骤指示 */}
      <div className="card">
        <div className="card-head">
          <h2>登录进度</h2>
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {STEPS.map((label, i) => {
            const done = i < stepIndex
            const active = i === stepIndex
            return (
              <div
                key={label}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  padding: '7px 13px',
                  borderRadius: 20,
                  fontSize: 13,
                  border: '1px solid',
                  borderColor: done ? 'rgba(63,185,80,.4)' : active ? 'rgba(79,140,255,.45)' : 'var(--border)',
                  background: done ? 'rgba(63,185,80,.1)' : active ? 'rgba(79,140,255,.11)' : 'transparent',
                  color: done ? '#7ee787' : active ? '#a8c8ff' : 'var(--text-faint)',
                }}
              >
                <span>{done ? '✓' : i + 1}</span>
                {label}
              </div>
            )
          })}
        </div>
      </div>

      {/* 第一步：发起授权 */}
      {!sess && (
        <div className="card">
          <div className="card-head">
            <h2>第 1 步 · 发起设备授权</h2>
          </div>
          <div className="field">
            <label>账号区域</label>
            <select value={region} onChange={(e) => setRegion(e.target.value as 'cn' | 'global')} style={{ maxWidth: 340 }}>
              <option value="cn">国内版（copilot.tencent.com / codebuddy.cn）</option>
              <option value="global">国际版（workbuddy.ai）</option>
            </select>
            <div className="desc">
              国内版走腾讯 CodeBuddy（codebuddy.cn），国际版走 WorkBuddy Global（workbuddy.ai）。请选择与你账号匹配的区域。
            </div>
          </div>
          <p className="text-dim" style={{ marginTop: 0, fontSize: 13 }}>
            点击下方按钮后，本面板会向对应区域的 WorkBuddy 申请一个一次性授权链接（有效期约 15 分钟），
            你在浏览器中打开并完成登录即可，无需在服务器上执行任何命令。
          </p>
          <Alert kind="info">
            <strong>前置要求</strong>
            <ul style={{ margin: '5px 0 0', paddingLeft: 18 }}>
              <li>
                需要一台能访问
                <span className="mono">{region === 'global' ? ' workbuddy.ai ' : ' copilot.tencent.com '}</span>
                的服务器（本面板需能出网）
              </li>
              <li>准备一个已注册的 WorkBuddy 账号用于授权</li>
              <li>多账号：重复本流程即可，每次会生成独立的授权链接</li>
            </ul>
          </Alert>
          <button className="btn btn-primary" onClick={() => void start()} disabled={busy || writeDisabled}>
            {busy ? <Spinner /> : '🔑'} 发起授权（{region === 'global' ? '国际版' : '国内版'}）
          </button>
        </div>
      )}

      {/* 第二步：浏览器登录 */}
      {sess && sess.status === 'pending' && (
        <div className="card">
          <div className="card-head">
            <h2>第 2 步 · 在浏览器中完成登录</h2>
            <Spinner label="等待授权中…" />
          </div>
          <p className="text-dim" style={{ marginTop: 0, fontSize: 13 }}>
            打开下面的链接，完成登录授权。本页面会自动检测结果，成功后无需手动操作。
          </p>
          <div className="url-box" style={{ marginBottom: 11 }}>
            {sess.auth_url}
          </div>
          <div className="page-actions">
            <a className="btn btn-primary" href={sess.auth_url} target="_blank" rel="noopener noreferrer">
              🔗 打开授权页面
            </a>
            <button className="btn" onClick={() => void copyURL()}>
              {copied ? '✓ 已复制' : '📋 复制链接'}
            </button>
            <button className="btn" onClick={() => void poll(sess.id)}>
              🔄 立即检查
            </button>
            <button className="btn btn-ghost" onClick={() => void cancel()}>
              取消
            </button>
          </div>
          <div className="desc" style={{ marginTop: 12 }}>
            提示：若浏览器已登录过 CodeBuddy，链接会直接跳到授权确认页；若长时间未完成，链接会过期，
            重新发起即可。登录状态每 2.5 秒自动检查一次。
          </div>
        </div>
      )}

      {/* 第三步：完成 */}
      {sess && sess.status === 'success' && (
        <div className="card">
          <div className="card-head">
            <h2>第 3 步 · 保存凭证</h2>
          </div>
          <Alert kind="ok">
            登录成功！（{sess.region === 'global' ? '国际版' : '国内版'}）
            <div style={{ marginTop: 6 }}>
              账号：<strong>{sess.nickname || sess.uid?.slice(0, 8)}</strong>
              <span className="mono text-faint"> ({sess.uid})</span>
            </div>
          </Alert>
          {sess.file && (
            <dl className="kv">
              <dt>凭证文件</dt>
              <dd className="mono">{sess.file}</dd>
              <dt>加载状态</dt>
              <dd>{sess.restart || '—'}</dd>
            </dl>
          )}
          <div className="page-actions" style={{ marginTop: 14 }}>
            <Link className="btn btn-primary" to="/accounts">
              👥 去账号管理
            </Link>
            <button
              className="btn"
              onClick={() => {
                setSess(null)
                setManualUID(null)
                void start()
              }}
            >
              ➕ 再添加一个账号
            </button>
          </div>
        </div>
      )}

      {/* 失败 / 过期 / 取消 */}
      {sess && (sess.status === 'error' || sess.status === 'expired' || sess.status === 'cancelled') && (
        <div className="card">
          <div className="card-head">
            <h2>登录未完成</h2>
          </div>
          <Alert kind={sess.status === 'cancelled' ? 'info' : 'error'}>
            {sess.message || '登录未完成'}
            {sess.status === 'error' && (
              <div style={{ marginTop: 6, fontSize: 12.5 }}>
                常见原因：未在浏览器完成登录就点了检查、登录页报错、或服务器无法访问上游。
              </div>
            )}
          </Alert>
          <div className="page-actions" style={{ marginTop: 14 }}>
            <button
              className="btn btn-primary"
              onClick={() => {
                setSess(null)
                void start()
              }}
            >
              🔄 重新发起
            </button>
            <Link className="btn" to="/accounts">
              返回账号管理
            </Link>
          </div>
          {manualUID && <div className="desc">会话：{manualUID}</div>}
        </div>
      )}

      <div className="card">
        <div className="card-head">
          <h2>其他添加方式</h2>
        </div>
        <p className="text-dim" style={{ marginTop: 0, fontSize: 13 }}>
          如果服务器无法直连上游（本面板发起授权失败），仍可用原有方式在网关目录执行
          <span className="mono"> ./login.sh </span>完成登录，然后在本面板「账号管理 → 导入凭证」中粘贴生成的
          <span className="mono"> auths/workbuddy-*.json </span>内容。
        </p>
        <button className="btn" onClick={() => setManualUID(manualUID ? null : 'hint')}>
          ℹ️ 了解导入方式
        </button>
        {manualUID && (
          <div className="muted-box" style={{ marginTop: 11 }}>
            {`# 在网关目录执行
cd /root/workbuddy2api && ./login.sh

# 然后把凭证内容复制出来
cat auths/workbuddy-<uid>.json`}
          </div>
        )}
      </div>
    </>
  )
}
