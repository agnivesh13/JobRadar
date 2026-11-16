import { useEffect, useRef, useState } from 'react'
import { api } from './api'
import { Badge, Button, ErrorNote } from './ui'

const MAX_LINES = 500

const outcomeStyle = {
  ok: 'bg-emerald-50 text-emerald-700 ring-emerald-200',
  new: 'bg-indigo-50 text-indigo-700 ring-indigo-200',
  unchanged: 'bg-slate-50 text-slate-500 ring-slate-200',
  info: 'bg-sky-50 text-sky-700 ring-sky-200',
  retry: 'bg-amber-50 text-amber-800 ring-amber-200',
  blocked: 'bg-amber-50 text-amber-800 ring-amber-200',
  error: 'bg-rose-50 text-rose-700 ring-rose-200',
  dead: 'bg-rose-50 text-rose-700 ring-rose-200',
}

const time = (iso) => new Date(iso).toLocaleTimeString([], { hour12: false })
const shortURL = (u) => (u || '').replace(/^https?:\/\//, '').slice(0, 70)

// useLive opens the Server-Sent Events stream and keeps the last 500 lines.
export function useLive(searchId = '') {
  const [lines, setLines] = useState([])
  const [status, setStatus] = useState(null)
  const [connected, setConnected] = useState(false)
  const held = useRef(false)

  useEffect(() => {
    setLines([])
    // Start with recent history so the feed is not empty on open.
    api.events({ search_id: searchId, limit: searchId ? 500 : 100 }).then((d) => setLines((cur) => (cur.length ? cur : d.events.map((e) => ({ ...e, id: 'h' + e.id }))))).catch(() => {})
    const es = new EventSource('/api/live' + (searchId ? `?search_id=${encodeURIComponent(searchId)}` : ''))
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false) // the browser reconnects by itself
    es.addEventListener('status', (m) => setStatus(JSON.parse(m.data)))
    es.addEventListener('crawl', (m) => {
      if (held.current) return
      const e = JSON.parse(m.data)
      setLines((cur) => [e, ...cur].slice(0, MAX_LINES))
    })
    return () => es.close()
  }, [searchId])

  return { lines, status, connected, hold: (v) => { held.current = v } }
}

export function EventLine({ e }) {
  return (
    <div className="flex items-baseline gap-3 px-3 py-1.5 text-sm">
      <span className="w-16 shrink-0 font-mono text-xs tabular-nums text-slate-400">{time(e.ts)}</span>
      <span className="w-20 shrink-0 text-xs font-medium uppercase tracking-wide text-slate-500">{e.stage}</span>
      <Badge className={`shrink-0 ${outcomeStyle[e.outcome] || outcomeStyle.info}`}>{e.outcome}</Badge>
      <span className="w-40 shrink-0 truncate font-medium text-slate-800" title={e.company}>{e.company || '—'}</span>
      <span className="min-w-0 flex-1 truncate text-slate-600" title={`${e.detail || ''} ${e.url || ''}`}>
        {e.detail}{e.detail && e.url ? ' · ' : ''}<span className="text-slate-400">{shortURL(e.url)}</span>
      </span>
      {e.duration_ms > 0 && <span className="shrink-0 font-mono text-xs tabular-nums text-slate-400">{e.duration_ms} ms</span>}
    </div>
  )
}

function Counter({ label, value, tone = 'text-slate-900' }) {
  return (
    <div className="rounded-lg bg-white px-4 py-3 shadow-sm ring-1 ring-slate-200">
      <p className="text-xs font-medium uppercase tracking-wide text-slate-500">{label}</p>
      <p className={`mt-1 text-2xl font-semibold tabular-nums ${tone}`}>{value ?? '—'}</p>
    </div>
  )
}

export default function Live() {
  const { lines, status, connected, hold } = useLive()
  const [error, setError] = useState(null)
  const [frozen, setFrozen] = useState(false)
  const [speed, setSpeed] = useState(null)

  const control = (patch) => api.setControl(patch).then(() => setError(null)).catch(setError)
  const c = status?.counters
  const workers = status ? Object.values(status.workers).reduce((a, b) => a + b, 0) : null
  const interval = speed ?? status?.interval_ms

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-6">
        <Counter label="Pages fetched" value={c?.pages_fetched} />
        <Counter label="Jobs found" value={c?.jobs_found} />
        <Counter label="New matches" value={c?.matches} tone="text-indigo-700" />
        <Counter label="Errors" value={c?.errors} tone={c?.errors ? 'text-rose-700' : 'text-slate-900'} />
        <Counter label="Queue depth" value={status?.queue_depth} />
        <Counter label="Active workers" value={workers} />
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg bg-white px-4 py-3 shadow-sm ring-1 ring-slate-200">
        <Badge className={connected ? 'bg-emerald-50 text-emerald-700 ring-emerald-200' : 'bg-rose-50 text-rose-700 ring-rose-200'}>
          {connected ? 'live' : 'reconnecting…'}
        </Badge>
        {status?.paused
          ? <Button variant="primary" onClick={() => control({ paused: false })}>Resume crawling</Button>
          : <Button onClick={() => control({ paused: true })}>Pause crawling</Button>}
        <label className="flex items-center gap-2 text-sm text-slate-600">
          Gap between requests to one site
          <input type="range" min={status?.min_interval_ms || 1000} max="10000" step="500" value={interval || 1000}
            onChange={(e) => setSpeed(Number(e.target.value))}
            onMouseUp={() => speed && control({ interval_ms: speed }).then(() => setSpeed(null))}
            onKeyUp={() => speed && control({ interval_ms: speed }).then(() => setSpeed(null))} />
          <span className="w-12 tabular-nums">{interval ? (interval / 1000).toFixed(1) : '—'} s</span>
        </label>
        {status && (
          <span className="text-sm text-slate-500">
            {Object.entries(status.workers).map(([k, n]) => `${n} ${k}`).join(' · ') || 'no workers running'}
            {status.busy > 0 && ` · ${status.busy} tasks in flight`}
          </span>
        )}
        <label className="ml-auto flex items-center gap-2 text-sm text-slate-500">
          <input type="checkbox" checked={frozen} onChange={(e) => { setFrozen(e.target.checked); hold(e.target.checked) }} className="rounded border-slate-300" />
          Freeze feed
        </label>
      </div>
      {status?.paused && <p className="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 ring-1 ring-amber-200">Crawling is paused. Tasks already in the queue wait there and resume when you press Resume.</p>}
      <ErrorNote error={error} />

      <div className="divide-y divide-slate-100 rounded-lg bg-white shadow-sm ring-1 ring-slate-200">
        {lines.map((e) => <EventLine key={e.id} e={e} />)}
        {lines.length === 0 && <p className="px-3 py-10 text-center text-slate-400">No crawl activity yet. Events appear here as companies are crawled.</p>}
      </div>
      <p className="text-xs text-slate-400">Showing the last {MAX_LINES} lines at most. The full log is in the crawl_events table.</p>
    </div>
  )
}
