import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import { EventLine, useLive } from './Live'
import { Badge, Button, ErrorNote, inputClass, timeAgo } from './ui'

const PAGE = 100
const TIERS = ['unknown', 'high', 'medium', 'low']

// SearchPanel streams one deep search: domain resolved, careers page found,
// board detected, pages crawled, jobs found.
function SearchPanel({ search, onClose, onDone }) {
  const [state, setState] = useState(search)
  const { lines } = useLive(search.search_id)
  const finished = lines.find((e) => e.stage === 'score')
  const jobs = lines.filter((e) => e.stage === 'extract').reduce((n, e) => n + (e.n || 0), 0)
  const matches = lines.filter((e) => e.stage === 'match').length

  useEffect(() => {
    if (state.state === 'failed' || finished) return
    const t = setInterval(() => api.search(search.search_id).then(setState).catch(() => {}), 1500)
    return () => clearInterval(t)
  }, [search.search_id, state.state, finished])
  useEffect(() => { if (finished) onDone?.() }, [finished]) // eslint-disable-line

  const pick = (domain) => api.pickCandidate(search.search_id, domain).then(setState).catch(() => {})

  return (
    <section className="rounded-lg bg-white shadow-sm ring-1 ring-indigo-200">
      <div className="flex flex-wrap items-center gap-3 border-b border-slate-100 px-4 py-3">
        <h2 className="text-sm font-semibold text-slate-900">Deep search: {search.name}</h2>
        {finished
          ? <Badge className="bg-emerald-50 text-emerald-700 ring-emerald-200">finished · {jobs} jobs found · {matches} match you</Badge>
          : state.state === 'failed'
            ? <Badge className="bg-rose-50 text-rose-700 ring-rose-200">failed</Badge>
            : <Badge className="bg-amber-50 text-amber-800 ring-amber-200">{state.state === 'pick' ? 'waiting for your choice' : state.state === 'crawling' ? `crawling · ${jobs} jobs so far` : 'resolving…'}</Badge>}
        {finished && state.company_id && <a href={`#/jobs?company=${state.company_id}`} className="text-sm font-medium text-indigo-600 hover:underline">View its jobs →</a>}
        <Button className="ml-auto" onClick={onClose}>Close</Button>
      </div>
      {state.state === 'failed' && <p className="px-4 py-3 text-sm text-rose-700">{state.error}</p>}
      {state.state === 'pick' && (
        <div className="space-y-2 px-4 py-3">
          <p className="text-sm text-slate-600">More than one website could be this company. Which one?</p>
          {state.candidates.map((c) => (
            <button key={c.domain} onClick={() => pick(c.domain)} className="block w-full rounded-md px-3 py-2 text-left text-sm ring-1 ring-slate-200 hover:bg-indigo-50">
              <span className="font-medium text-slate-900">{c.domain}</span> <span className="text-slate-500">· {c.title}</span>
            </button>
          ))}
        </div>
      )}
      <div className="max-h-80 divide-y divide-slate-100 overflow-y-auto">
        {[...lines].reverse().map((e) => <EventLine key={e.id} e={e} />)}
      </div>
    </section>
  )
}

export default function Companies({ onChanged }) {
  const [q, setQ] = useState('')
  const [page, setPage] = useState(0)
  const [data, setData] = useState({ companies: [], total: 0 })
  const [error, setError] = useState(null)
  const [search, setSearch] = useState(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => api.companies({ q, limit: PAGE, offset: page * PAGE }).then((d) => { setData(d); setError(null) }).catch(setError), [q, page])
  useEffect(() => { const t = setTimeout(load, 200); return () => clearTimeout(t) }, [load])
  useEffect(() => { const t = setInterval(load, 10000); return () => clearInterval(t) }, [load])

  const act = (fn) => fn().then(() => { load(); onChanged?.() }).catch(setError)
  const exact = data.companies.some((c) => c.name.toLowerCase() === q.trim().toLowerCase())

  const deepSearch = async (e) => {
    e.preventDefault()
    if (q.trim().length < 2) return
    setBusy(true)
    try { setSearch(await api.deepSearch(q.trim())); setError(null) } catch (err) { setError(err) } finally { setBusy(false) }
  }

  const pages = Math.ceil(data.total / PAGE)
  return (
    <div className="space-y-4">
      <form onSubmit={deepSearch} className="flex flex-wrap items-center gap-2">
        <input className={`${inputClass} max-w-md flex-1`} placeholder="Type a company name or website" value={q} onChange={(e) => { setQ(e.target.value); setPage(0) }} />
        <Button variant="primary" type="submit" disabled={busy || q.trim().length < 2}>{exact ? 'Deep search again' : 'Deep search this company'}</Button>
        <span className="text-sm text-slate-500">Filters the list as you type. Deep search finds the company’s site, then crawls it for jobs.</span>
      </form>
      <ErrorNote error={error} />
      {search && <SearchPanel key={search.search_id} search={search} onClose={() => setSearch(null)} onDone={() => { load(); onChanged?.() }} />}

      <div className="overflow-x-auto rounded-lg bg-white shadow-sm ring-1 ring-slate-200">
        <table className="min-w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs font-medium uppercase tracking-wide text-slate-500">
            <tr>
              <th className="px-3 py-2">Company</th>
              <th className="px-3 py-2">Source</th>
              <th className="px-3 py-2">Last crawl</th>
              <th className="px-3 py-2 text-right">Open roles</th>
              <th className="px-3 py-2 text-right">Match you</th>
              <th className="px-3 py-2">Pay tier</th>
              <th className="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {data.companies.map((c) => (
              <tr key={c.id} className={c.status === 'manual' ? 'bg-amber-50/40' : ''}>
                <td className="px-3 py-2">
                  <span className="font-medium text-slate-900">{c.name}</span>
                  {c.domain && <span className="ml-2 text-xs text-slate-400">{c.domain}</span>}
                  {c.status === 'manual' && <Badge className="ml-2 bg-amber-50 text-amber-800 ring-amber-200" title={c.status_reason}>manual</Badge>}
                  {c.status === 'active' && c.status_reason && <p className="text-xs text-rose-600">{c.status_reason}</p>}
                  {c.status === 'manual' && <p className="text-xs text-amber-700">{c.status_reason}</p>}
                </td>
                <td className="whitespace-nowrap px-3 py-2 text-slate-600">
                  {c.board_type}{c.board_token ? ` · ${c.board_token}` : ''}
                  <span className="ml-2 text-xs text-slate-400" title={c.source_detail}>via {c.source.replace('_', ' ')}</span>
                </td>
                <td className="whitespace-nowrap px-3 py-2 text-slate-500">{c.in_flight ? <Badge className="bg-sky-50 text-sky-700 ring-sky-200">crawling…</Badge> : c.last_crawled_at ? timeAgo(c.last_crawled_at) : 'never'}</td>
                <td className="px-3 py-2 text-right tabular-nums text-slate-600">{c.open_jobs}</td>
                <td className="px-3 py-2 text-right tabular-nums">{c.matching > 0 ? <a href={`#/jobs?company=${c.id}`} className="font-medium text-indigo-600 hover:underline">{c.matching}</a> : <span className="text-slate-300">0</span>}</td>
                <td className="px-3 py-2">
                  <select value={c.pay_tier} onChange={(e) => act(() => api.patchCompany(c.id, { pay_tier: e.target.value }))}
                    className="rounded-md border-0 py-1 pl-2 pr-7 text-sm text-slate-700 ring-1 ring-inset ring-slate-300">
                    {TIERS.map((t) => <option key={t} value={t}>{t}</option>)}
                  </select>
                </td>
                <td className="whitespace-nowrap px-3 py-2 text-right">
                  {c.status === 'manual'
                    ? <Button onClick={() => act(() => api.patchCompany(c.id, { status: 'active' }))}>Try again</Button>
                    : <Button disabled={c.in_flight} onClick={() => act(() => api.crawlCompany(c.id))}>Crawl now</Button>}
                  <Button variant="danger" className="ml-2" onClick={() => act(() => api.removeCompany(c.id))}>Remove</Button>
                </td>
              </tr>
            ))}
            {data.companies.length === 0 && <tr><td colSpan={7} className="px-3 py-10 text-center text-slate-400">No company in the list matches. Press “Deep search this company” to find and crawl it.</td></tr>}
          </tbody>
        </table>
      </div>
      <div className="flex items-center justify-between text-sm text-slate-500">
        <span>{data.total} companies</span>
        {pages > 1 && (
          <span className="flex items-center gap-2">
            <Button disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</Button>
            <span className="tabular-nums">Page {page + 1} of {pages}</span>
            <Button disabled={page + 1 >= pages} onClick={() => setPage(page + 1)}>Next</Button>
          </span>
        )}
      </div>
    </div>
  )
}
