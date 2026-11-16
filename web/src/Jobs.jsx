import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import { Badge, Button, ErrorNote, FitScore, LEVELS, StatusSelect, formatSalary, inputClass, timeAgo } from './ui'

const TABS = [
  ['stated', 'Salary stated'],
  ['unknown', 'Salary unknown'],
  ['all', 'All matches'],
]
const PAGE = 50

// #/jobs?company=12 (from the Companies screen) shows one company's postings.
const companyFromHash = () => new URLSearchParams(window.location.hash.split('?')[1] || '').get('company') || ''

export default function Jobs({ onChanged }) {
  const [companyId, setCompanyId] = useState(companyFromHash)
  const [tab, setTab] = useState(companyFromHash() ? 'all' : 'stated')
  const [filters, setFilters] = useState({ q: '', location: '', experience: '', days: '', status: '', sort: '', unmatched: false })
  const [page, setPage] = useState(0)
  const [data, setData] = useState({ jobs: [], total: 0 })
  const [counts, setCounts] = useState(null)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(true)
  const [openId, setOpenId] = useState(null)

  const load = useCallback(() => {
    setLoading(true)
    // "Salary unknown" is ranked by fit score, which carries the company signal.
    const sort = filters.sort || (tab === 'unknown' ? 'fit' : 'newest')
    api
      .jobs({ ...filters, sort, tab, company_id: companyId, limit: PAGE, offset: page * PAGE })
      .then((d) => { setData(d); setError(null) })
      .catch(setError)
      .finally(() => setLoading(false))
    api.stats().then((d) => setCounts(d)).catch(() => {})
  }, [tab, filters, page, companyId])

  useEffect(() => { const t = setTimeout(load, 200); return () => clearTimeout(t) }, [load])

  const set = (k) => (e) => { setPage(0); setFilters((f) => ({ ...f, [k]: e.target.type === 'checkbox' ? e.target.checked : e.target.value })) }

  const setStatus = async (job, status) => {
    try {
      const updated = await api.saveApplication(job.id, { status })
      setData((d) => ({ ...d, jobs: d.jobs.map((j) => (j.id === job.id ? updated : j)).filter((j) => filters.status || j.status !== 'skipped') }))
      onChanged?.()
    } catch (e) { setError(e) }
  }

  const st = counts?.stats
  const tabCount = { stated: st?.matched_stated, unknown: st?.matched_unknown, all: st?.matched }
  const pages = Math.ceil(data.total / PAGE)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex gap-1 rounded-lg bg-slate-100 p-1">
          {TABS.map(([k, label]) => (
            <button key={k} onClick={() => { setTab(k); setPage(0) }}
              className={`rounded-md px-3 py-1.5 text-sm font-medium ${tab === k ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500 hover:text-slate-700'}`}>
              {label}
              {tabCount[k] != null && <span className="ml-1.5 tabular-nums text-slate-400">{tabCount[k]}</span>}
            </button>
          ))}
        </div>
        {counts?.rescoring && <Badge className="bg-amber-50 text-amber-800 ring-amber-200">Re-scoring jobs…</Badge>}
      </div>

      <div className="grid grid-cols-2 gap-2 md:grid-cols-7">
        <input className={`${inputClass} col-span-2`} placeholder="Search title or company" value={filters.q} onChange={set('q')} />
        <input className={inputClass} placeholder="Location" value={filters.location} onChange={set('location')} />
        <select className={inputClass} value={filters.experience} onChange={set('experience')}>
          <option value="">Any level</option>
          <option value="intern">Intern</option>
          <option value="new_grad">New grad</option>
          <option value="junior">0–2 years</option>
        </select>
        <select className={inputClass} value={filters.days} onChange={set('days')}>
          <option value="">Seen any time</option>
          <option value="1">Last 1 day</option>
          <option value="3">Last 3 days</option>
          <option value="7">Last 7 days</option>
          <option value="30">Last 30 days</option>
        </select>
        <select className={inputClass} value={filters.status} onChange={set('status')}>
          <option value="">Hide skipped</option>
          <option value="not_applied">Not applied</option>
          <option value="saved">Saved</option>
          <option value="applied">Applied</option>
          <option value="skipped">Skipped</option>
          <option value="any">Everything</option>
        </select>
        <select className={inputClass} value={filters.sort} onChange={set('sort')}>
          <option value="">Default order</option>
          <option value="newest">Newest first</option>
          <option value="fit">Best fit first</option>
          <option value="salary">Highest salary first</option>
        </select>
      </div>
      <label className="flex items-center gap-2 text-sm text-slate-500">
        <input type="checkbox" checked={filters.unmatched} onChange={set('unmatched')} className="rounded border-slate-300" />
        Also show postings that fail my role, level and location filters
      </label>

      {companyId && (
        <p className="flex items-center gap-3 rounded-md bg-indigo-50 px-3 py-2 text-sm text-indigo-800 ring-1 ring-indigo-200">
          Showing one company only: {data.jobs[0]?.company || `#${companyId}`}
          <Button onClick={() => { setCompanyId(''); window.location.hash = '#/jobs' }}>Show all companies</Button>
        </p>
      )}
      <ErrorNote error={error} />

      <div className="overflow-x-auto rounded-lg bg-white shadow-sm ring-1 ring-slate-200">
        <table className="min-w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs font-medium uppercase tracking-wide text-slate-500">
            <tr>
              <th className="px-3 py-2">Company</th>
              <th className="px-3 py-2">Title</th>
              <th className="px-3 py-2">Location</th>
              <th className="px-3 py-2">Level</th>
              <th className="px-3 py-2">Salary</th>
              <th className="px-3 py-2">Fit</th>
              <th className="px-3 py-2">First seen</th>
              <th className="px-3 py-2">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {data.jobs.map((j) => (
              <tr key={j.id} onClick={() => setOpenId(j.id)} className="cursor-pointer hover:bg-indigo-50/40">
                <td className="whitespace-nowrap px-3 py-2 font-medium text-slate-900">{j.company}</td>
                <td className="max-w-md px-3 py-2">
                  <span className="text-slate-800">{j.title}</span>
                  {j.updated && <Badge className="ml-2 bg-sky-50 text-sky-700 ring-sky-200">updated</Badge>}
                  {j.flags?.includes('batch_mismatch') && <Badge className="ml-2 bg-rose-50 text-rose-700 ring-rose-200" title="The posting names a graduation batch other than yours">other batch</Badge>}
                </td>
                <td className="max-w-48 truncate px-3 py-2 text-slate-600" title={j.location}>{j.location || '—'}</td>
                <td className="whitespace-nowrap px-3 py-2 text-slate-600">{LEVELS[j.experience] ?? j.experience}</td>
                <td className="whitespace-nowrap px-3 py-2">
                  {formatSalary(j)
                    ? <span className={j.salary_state === 'below' ? 'text-slate-400' : 'font-medium text-emerald-700'} title={j.salary_text || ''}>{formatSalary(j)}</span>
                    : <span className="text-slate-400">not stated</span>}
                </td>
                <td className="px-3 py-2"><FitScore score={j.fit_score} reason={j.fit_reason} /></td>
                <td className="whitespace-nowrap px-3 py-2 text-slate-500">{timeAgo(j.first_seen_at)}</td>
                <td className="px-3 py-2"><StatusSelect status={j.status} onChange={(s) => setStatus(j, s)} /></td>
              </tr>
            ))}
            {!loading && data.jobs.length === 0 && (
              <tr><td colSpan={8} className="px-3 py-10 text-center text-slate-400">No postings match. Loosen a filter, or check Settings.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between text-sm text-slate-500">
        <span>{data.total} postings{loading ? ' · loading…' : ''}</span>
        {pages > 1 && (
          <span className="flex items-center gap-2">
            <Button disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</Button>
            <span className="tabular-nums">Page {page + 1} of {pages}</span>
            <Button disabled={page + 1 >= pages} onClick={() => setPage(page + 1)}>Next</Button>
          </span>
        )}
      </div>

      {openId && <JobDrawer id={openId} onClose={() => setOpenId(null)} onSaved={() => { load(); onChanged?.() }} />}
    </div>
  )
}

// JobDrawer shows one posting with its description and the tracker fields.
export function JobDrawer({ id, onClose, onSaved }) {
  const [job, setJob] = useState(null)
  const [app, setApp] = useState(null)
  const [error, setError] = useState(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    setJob(null)
    Promise.all([api.job(id), api.tracker()])
      .then(([j, t]) => {
        setJob(j)
        const a = t.items.find((x) => x.job.id === id)?.application
        setApp({ status: j.status, contact_name: a?.contact_name || '', contact_email: a?.contact_email || '', notes: a?.notes || '', follow_up_on: a?.follow_up_on || '' })
      })
      .catch(setError)
  }, [id])

  useEffect(() => {
    const onKey = (e) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const save = async (patch) => {
    try {
      const next = { ...app, ...patch }
      setApp(next)
      await api.saveApplication(id, next.status === 'not_applied' ? { status: 'not_applied' } : next)
      setSaved(true); setTimeout(() => setSaved(false), 1500)
      onSaved?.()
    } catch (e) { setError(e) }
  }
  const field = (k) => ({ value: app[k], onChange: (e) => setApp({ ...app, [k]: e.target.value }), onBlur: () => app.status !== 'not_applied' && save({}) })

  return (
    <div className="fixed inset-0 z-20 flex justify-end bg-slate-900/30" onClick={onClose}>
      <aside className="h-full w-full max-w-2xl overflow-y-auto bg-white p-6 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-sm font-medium text-indigo-600">{job?.company}</p>
            <h2 className="mt-0.5 text-xl font-semibold text-slate-900">{job?.title || 'Loading…'}</h2>
            {job && <p className="mt-1 text-sm text-slate-500">{[job.location, LEVELS[job.experience] !== '—' && LEVELS[job.experience], formatSalary(job) || 'salary not stated'].filter(Boolean).join(' · ')}</p>}
          </div>
          <Button onClick={onClose} aria-label="Close">Close</Button>
        </div>
        <ErrorNote error={error} />
        {job && app && (
          <>
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <a href={job.url} target="_blank" rel="noreferrer" className="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">Open posting ↗</a>
              {job.alt_urls?.map((u, i) => <a key={u} href={u} target="_blank" rel="noreferrer" className="text-sm text-indigo-600 hover:underline">Other link {i + 1} ↗</a>)}
              <StatusSelect status={app.status} onChange={(s) => save({ status: s })} />
              {saved && <span className="text-xs text-emerald-600">Saved</span>}
              {job.closed && <Badge className="bg-slate-100 text-slate-500 ring-slate-200">posting closed</Badge>}
            </div>
            <p className="mt-3 text-sm text-slate-600"><span className="font-medium text-slate-800">Fit {job.fit_score}/100.</span> {job.fit_reason}</p>
            {job.salary_text && <p className="mt-1 text-sm text-slate-500">Salary read from: “{job.salary_text}”</p>}

            <div className={`mt-5 grid grid-cols-2 gap-3 rounded-lg bg-slate-50 p-4 ${app.status === 'not_applied' ? 'opacity-60' : ''}`}>
              <p className="col-span-2 text-xs font-medium uppercase tracking-wide text-slate-500">
                Tracker {app.status === 'not_applied' && '· pick a status above to start tracking'}
              </p>
              <label className="text-sm text-slate-600">Contact name<input className={`${inputClass} mt-1`} disabled={app.status === 'not_applied'} {...field('contact_name')} /></label>
              <label className="text-sm text-slate-600">Contact email<input type="email" className={`${inputClass} mt-1`} disabled={app.status === 'not_applied'} {...field('contact_email')} /></label>
              <label className="text-sm text-slate-600">Follow up on<input type="date" className={`${inputClass} mt-1`} disabled={app.status === 'not_applied'} {...field('follow_up_on')} /></label>
              <label className="col-span-2 text-sm text-slate-600">Notes<textarea rows={3} className={`${inputClass} mt-1`} disabled={app.status === 'not_applied'} {...field('notes')} /></label>
            </div>

            <h3 className="mt-6 text-sm font-semibold text-slate-900">Description</h3>
            <p className="mt-2 whitespace-pre-line text-sm leading-6 text-slate-700">{job.description || 'No description captured.'}</p>
          </>
        )}
      </aside>
    </div>
  )
}
