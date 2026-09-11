// Playground.tsx 聊天测试台：验证网关的 OpenAI 兼容接口、流式输出与会话粘性。
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError, streamChat } from '../api'
import type { ChatMessage, Model } from '../types'
import { Alert, fmtNum, Spinner } from '../ui'

interface Bubble {
  role: 'user' | 'assistant'
  content: string
  reasoning?: string
  /** streaming 表示该气泡仍在接收增量。 */
  streaming?: boolean
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }
  stats?: { elapsed_ms?: number; ttfb_ms?: number }
  error?: string
}

export default function Playground() {
  const [models, setModels] = useState<Model[]>([])
  const [model, setModel] = useState('')
  const [input, setInput] = useState('你好，请用一句话自我介绍')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [messages, setMessages] = useState<Bubble[]>([])
  const [stream, setStream] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [temperature, setTemperature] = useState<string>('')
  const [maxTokens, setMaxTokens] = useState<string>('')
  const [stickyEnabled, setStickyEnabled] = useState(true)
  const [conversationId, setConversationId] = useState(() => `playground-${Math.random().toString(36).slice(2, 10)}`)
  const [showRaw, setShowRaw] = useState<string | null>(null)

  const abortRef = useRef<(() => void) | null>(null)
  const logRef = useRef<HTMLDivElement>(null)

  const loadModels = async () => {
    try {
      const res = await api.models()
      const list = res.data ?? []
      setModels(list)
      if (list.length > 0 && !model) setModel(list[0].id)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '加载模型列表失败')
    }
  }

  useEffect(() => {
    void loadModels()
    // 只在首次挂载时拉取；model 由用户选择，不参与依赖避免重复请求。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 新消息到达时自动滚到底部。
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight
  }, [messages])

  // 卸载时中止进行中的流。
  useEffect(() => {
    return () => abortRef.current?.()
  }, [])

  const extraParams = useMemo(() => {
    const extra: Record<string, unknown> = {}
    const t = parseFloat(temperature)
    if (temperature.trim() !== '' && !Number.isNaN(t)) extra.temperature = t
    const m = parseInt(maxTokens, 10)
    if (maxTokens.trim() !== '' && !Number.isNaN(m) && m > 0) extra.max_tokens = m
    return extra
  }, [temperature, maxTokens])

  /** buildMessages 组装发往网关的消息数组（不含正在流式输出的空助手气泡）。 */
  const buildMessages = (): ChatMessage[] => {
    const out: ChatMessage[] = []
    if (systemPrompt.trim()) out.push({ role: 'system', content: systemPrompt.trim() })
    for (const m of messages) {
      if (m.streaming && !m.content) continue
      if (m.error) continue
      out.push({ role: m.role, content: m.content })
    }
    return out
  }

  const send = async () => {
    const text = input.trim()
    if (!text || busy) return
    if (!model) {
      setError('请先选择模型')
      return
    }
    setError(null)
    const userBubble: Bubble = { role: 'user', content: text }
    const history = [...buildMessages(), { role: 'user' as const, content: text }]
    setMessages((prev) => [...prev, userBubble, { role: 'assistant', content: '', streaming: true }])
    setInput('')
    setBusy(true)

    if (stream) {
      abortRef.current = streamChat(
        { model, messages: history, extra: extraParams, conversationId: stickyEnabled ? conversationId : undefined },
        (d) => {
          setMessages((prev) => {
            const next = [...prev]
            const last = next[next.length - 1]
            if (!last || last.role !== 'assistant') return prev
            if (d.error) {
              next[next.length - 1] = { ...last, error: d.error, streaming: false }
              return next
            }
            // 累积文本与推理；usage/统计只在新帧带来时更新。
            // streaming 由流关闭（onClose）统一置 false，避免中间帧提前收起光标。
            next[next.length - 1] = {
              ...last,
              content: last.content + (d.content ?? ''),
              reasoning: (last.reasoning ?? '') + (d.reasoning ?? ''),
              usage: d.usage ?? last.usage,
              stats:
                d.elapsed_ms !== undefined
                  ? { elapsed_ms: d.elapsed_ms, ttfb_ms: d.ttfb_ms }
                  : last.stats,
            }
            return next
          })
        },
        (msg) => {
          setMessages((prev) => {
            const next = [...prev]
            const last = next[next.length - 1]
            if (last?.role === 'assistant') next[next.length - 1] = { ...last, error: msg, streaming: false }
            return next
          })
        },
        () => {
          setBusy(false)
          abortRef.current = null
          setMessages((prev) => {
            const next = [...prev]
            const last = next[next.length - 1]
            if (last?.streaming) next[next.length - 1] = { ...last, streaming: false }
            return next
          })
        },
      )
    } else {
      try {
        const res = await api.chat({
          model,
          messages: history,
          extra: extraParams,
          conversationId: stickyEnabled ? conversationId : undefined,
        })
        setMessages((prev) => {
          const next = [...prev]
          next[next.length - 1] = {
            role: 'assistant',
            content: res.result.content,
            reasoning: res.result.reasoning_content,
            usage: {
              prompt_tokens: res.result.prompt_tokens,
              completion_tokens: res.result.completion_tokens,
              total_tokens: res.result.total_tokens,
            },
            stats: { elapsed_ms: res.elapsed_ms },
          }
          return next
        })
      } catch (err) {
        const msg = err instanceof ApiError ? err.message : '请求失败'
        setMessages((prev) => {
          const next = [...prev]
          next[next.length - 1] = { role: 'assistant', content: '', error: msg }
          return next
        })
      } finally {
        setBusy(false)
      }
    }
  }

  const stop = () => {
    abortRef.current?.()
    abortRef.current = null
    setBusy(false)
  }

  const clearChat = () => {
    stop()
    setMessages([])
    setConversationId(`playground-${Math.random().toString(36).slice(2, 10)}`)
  }

  const currentModel = models.find((m) => m.id === model)

  return (
    <>
      <div className="page-head">
        <div>
          <h1>聊天测试</h1>
          <p>直接调用网关的 OpenAI 兼容接口，验证账号轮转、流式输出与推理内容</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={() => void loadModels()}>
            🔄 刷新模型列表
          </button>
          <button className="btn" onClick={clearChat} disabled={messages.length === 0}>
            🗑️ 清空对话
          </button>
        </div>
      </div>

      {error && (
        <Alert kind="error" onClose={() => setError(null)}>
          {error}
        </Alert>
      )}

      <div className="grid grid-2" style={{ alignItems: 'start' }}>
        <div className="card">
          <div className="card-head">
            <h2>会话设置</h2>
            {currentModel && (
              <span className="hint">
                上下文 {fmtNum(currentModel.context_length)}
                {currentModel.max_output_tokens ? ` · 最大输出 ${fmtNum(currentModel.max_output_tokens)}` : ''}
              </span>
            )}
          </div>

          <div className="field">
            <label>模型</label>
            <select value={model} onChange={(e) => setModel(e.target.value)}>
              {models.length === 0 && <option value="">（加载中或网关不可用）</option>}
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.id}
                </option>
              ))}
            </select>
          </div>

          <div className="field">
            <label>System Prompt（可选）</label>
            <textarea rows={2} value={systemPrompt} onChange={(e) => setSystemPrompt(e.target.value)} placeholder="给模型的系统指令…" />
          </div>

          <div className="row">
            <div className="field" style={{ flex: 1 }}>
              <label>temperature（留空用默认）</label>
              <input type="text" value={temperature} onChange={(e) => setTemperature(e.target.value)} placeholder="0 ~ 2" />
            </div>
            <div className="field" style={{ flex: 1 }}>
              <label>max_tokens（留空用默认）</label>
              <input type="text" value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} placeholder="例如 1024" />
            </div>
          </div>

          <div className="field">
            <label className="checkbox">
              <input type="checkbox" checked={stream} onChange={(e) => setStream(e.target.checked)} />
              流式输出（SSE）
            </label>
            <div className="desc">关闭后由网关聚合为单个响应返回，适合对比验证。</div>
          </div>

          <div className="field" style={{ marginBottom: 0 }}>
            <label className="checkbox">
              <input type="checkbox" checked={stickyEnabled} onChange={(e) => setStickyEnabled(e.target.checked)} />
              发送会话 ID（触发网关的会话粘性）
            </label>
            <div className="desc">
              带上 <span className="mono">conversation_id</span> 后，同一会话会尽量固定到同一账号，
              多轮对话不跳号。当前 ID：
              <span className="mono">{stickyEnabled ? conversationId : '（未发送）'}</span>
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <h2>对话</h2>
            {busy && <Spinner label={stream ? '接收中…' : '请求中…'} />}
          </div>

          <div className="chat-log" ref={logRef}>
            {messages.length === 0 && (
              <div className="empty" style={{ margin: 'auto' }}>
                发送一条消息开始测试。
                <div style={{ marginTop: 6, fontSize: 12 }}>请求会经由账号池自动选号转发到上游。</div>
              </div>
            )}
            {messages.map((m, i) => (
              <div key={i} className={`msg msg-${m.role}`}>
                <div className="msg-role">{m.role === 'user' ? '你' : 'AI'}</div>
                <div className="msg-body">
                  {m.reasoning && (
                    <div className="reasoning">
                      <div className="reasoning-label">推理过程</div>
                      {m.reasoning}
                    </div>
                  )}
                  {m.content && <pre>{m.content}</pre>}
                  {m.streaming && !m.content && !m.reasoning && (
                    <span className="text-faint" style={{ fontSize: 12.5 }}>
                      正在思考…
                    </span>
                  )}
                  {m.streaming && m.content && <span className="cursor" />}
                  {m.error && (
                    <Alert kind="error">
                      {m.error}
                      {m.error.includes('503') && (
                        <div style={{ marginTop: 5, fontSize: 12.5 }}>
                          所有账号可能都在冷却中，请到「仪表盘」查看账号池状态。
                        </div>
                      )}
                    </Alert>
                  )}
                  {(m.usage || m.stats) && !m.streaming && (
                    <div className="text-faint" style={{ fontSize: 11.5, marginTop: 6, display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                      {m.usage && (
                        <>
                          <span>输入 {m.usage.prompt_tokens} tok</span>
                          <span>输出 {m.usage.completion_tokens} tok</span>
                        </>
                      )}
                      {m.stats?.ttfb_ms ? <span>首字 {m.stats.ttfb_ms} ms</span> : null}
                      {m.stats?.elapsed_ms ? <span>总耗时 {m.stats.elapsed_ms} ms</span> : null}
                      {m.content && <button className="btn btn-ghost btn-sm" style={{ padding: '0 4px', fontSize: 11 }} onClick={() => setShowRaw(JSON.stringify(m, null, 2))}>
                        查看原始数据
                      </button>}
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>

          <div className="field" style={{ marginTop: 13, marginBottom: 0 }}>
            <textarea
              rows={3}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="输入消息… （Ctrl + Enter 发送）"
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault()
                  void send()
                }
              }}
            />
          </div>
          <div className="page-actions" style={{ marginTop: 11 }}>
            {busy ? (
              <button className="btn btn-danger" onClick={stop}>
                ⏹ 停止
              </button>
            ) : (
              <button className="btn btn-primary" onClick={() => void send()} disabled={!input.trim() || !model}>
                ▶️ 发送
              </button>
            )}
            <span className="hint">Ctrl + Enter 快捷发送</span>
          </div>
        </div>
      </div>

      {showRaw && (
        <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && setShowRaw(null)}>
          <div className="modal modal-wide">
            <div className="modal-head">
              <h2>原始响应数据</h2>
              <button className="btn btn-ghost btn-sm" onClick={() => setShowRaw(null)}>
                ✕
              </button>
            </div>
            <div className="muted-box" style={{ maxHeight: 420, overflow: 'auto', whiteSpace: 'pre-wrap' }}>{showRaw}</div>
          </div>
        </div>
      )}
    </>
  )
}
