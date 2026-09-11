// ui.tsx 通用展示组件与格式化工具。

import React, { useEffect } from 'react'

/** 把 Unix 秒格式化为本地时间字符串。 */
export function fmtTime(sec?: number): string {
  if (!sec || sec <= 0) return '—'
  const d = new Date(sec * 1000)
  const now = Date.now()
  const diff = d.getTime() - now
  const abs = Math.abs(diff)
  let rel = ''
  if (abs < 60_000) rel = diff > 0 ? '即将' : '刚刚'
  else if (abs < 3_600_000) rel = diff > 0 ? `${Math.round(abs / 60_000)} 分钟后` : `${Math.round(abs / 60_000)} 分钟前`
  else if (abs < 86_400_000) rel = diff > 0 ? `${Math.round(abs / 3_600_000)} 小时后` : `${Math.round(abs / 3_600_000)} 小时前`
  else rel = diff > 0 ? `${Math.round(abs / 86_400_000)} 天后` : `${Math.round(abs / 86_400_000)} 天前`
  return `${d.toLocaleString('zh-CN', { hour12: false })}（${rel}）`
}

/** 把 ISO 时间字符串格式化为本地时间（相对时间优先）。 */
export function fmtISO(iso?: string): string {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (!t || t <= 0) return '—'
  // Go 的零值时间（0001-01-01）按「无记录」处理。
  if (new Date(t).getFullYear() < 1980) return '—'
  const d = new Date(t)
  const diff = Date.now() - t
  if (diff < 0) return d.toLocaleString('zh-CN', { hour12: false })
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)} 小时前`
  if (diff < 30 * 86_400_000) return `${Math.round(diff / 86_400_000)} 天前`
  return d.toLocaleString('zh-CN', { hour12: false })
}

/** 格式化秒数为「1h 23m 45s」。 */
export function fmtDuration(sec?: number): string {
  if (sec === undefined || sec === null || sec <= 0) return '—'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  if (h > 0) return `${h}小时${m}分`
  if (m > 0) return `${m}分${s}秒`
  return `${s}秒`
}

/** 千分位数字。 */
export function fmtNum(n?: number): string {
  if (n === undefined || n === null) return '—'
  return n.toLocaleString('en-US')
}

/** uid 缩写展示。 */
export function shortUID(uid: string): string {
  return uid.length > 8 ? uid.slice(0, 8) : uid
}

/** 账号展示名：昵称优先，其次 uid 前缀。 */
export function displayName(a: { nickname?: string; uid: string }): string {
  return a.nickname || shortUID(a.uid)
}

/** 状态徽章配色与文案。 */
export function statusBadge(status: string): { cls: string; text: string } {
  switch (status) {
    case 'healthy':
      return { cls: 'badge-ok', text: '正常' }
    case 'cooling':
      return { cls: 'badge-warn', text: '冷却中' }
    case 'disabled':
      return { cls: 'badge-danger', text: '已禁用' }
    case 'token_expired':
      return { cls: 'badge-danger', text: 'Token 过期' }
    case 'gateway_unreachable':
      return { cls: 'badge-dim', text: '网关不可达' }
    case 'missing_credential':
      return { cls: 'badge-warn', text: '凭证缺失' }
    default:
      return { cls: 'badge-dim', text: '未加载' }
  }
}

/** 冷却原因的中文说明。 */
export function coolKindText(kind?: string): string {
  switch (kind) {
    case 'hard_credit':
      return '余额不足'
    case 'soft_rate':
      return '被限流'
    default:
      return '冷却'
  }
}

export function Badge({ cls, children }: { cls: string; children: React.ReactNode }) {
  return (
    <span className={`badge ${cls}`}>
      <span className="badge-dot" />
      {children}
    </span>
  )
}

export function Alert({
  kind,
  children,
  onClose,
}: {
  kind: 'error' | 'warn' | 'ok' | 'info'
  children: React.ReactNode
  onClose?: () => void
}) {
  const icon = { error: '⛔', warn: '⚠️', ok: '✅', info: 'ℹ️' }[kind]
  return (
    <div className={`alert alert-${kind}`}>
      <span className="alert-icon">{icon}</span>
      <div style={{ flex: 1, minWidth: 0 }}>{children}</div>
      {onClose && (
        <button className="btn btn-ghost btn-sm" onClick={onClose} aria-label="关闭">
          ✕
        </button>
      )}
    </div>
  )
}

export function Modal({
  title,
  onClose,
  children,
  wide,
  footer,
}: {
  title: string
  onClose: () => void
  children: React.ReactNode
  wide?: boolean
  footer?: React.ReactNode
}) {
  // Esc 关闭 + 打开期间锁定页面滚动。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = prev
    }
  }, [onClose])

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        // 只有点击遮罩本身才关闭，避免拖选文本误触。
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className={`modal ${wide ? 'modal-wide' : ''}`} role="dialog" aria-modal="true">
        <div className="modal-head">
          <h2>{title}</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose} aria-label="关闭">
            ✕
          </button>
        </div>
        {children}
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  )
}

export function Spinner({ label }: { label?: string }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
      <span className="spinner" />
      {label && <span className="text-dim">{label}</span>}
    </span>
  )
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <div className="empty">{children}</div>
}

/** ConfirmDialog 危险操作二次确认。 */
export function ConfirmDialog({
  title,
  message,
  confirmText = '确认',
  danger,
  onConfirm,
  onCancel,
  busy,
}: {
  title: string
  message: React.ReactNode
  confirmText?: string
  danger?: boolean
  onConfirm: () => void
  onCancel: () => void
  busy?: boolean
}) {
  return (
    <Modal
      title={title}
      onClose={onCancel}
      footer={
        <>
          <button className="btn" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button
            className={`btn ${danger ? 'btn-danger' : 'btn-primary'}`}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? <Spinner /> : null}
            {confirmText}
          </button>
        </>
      }
    >
      <div style={{ fontSize: 13.5, lineHeight: 1.7, color: 'var(--text-dim)' }}>{message}</div>
    </Modal>
  )
}
