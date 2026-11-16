import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import { JobDrawer } from './Jobs'
import { Badge, ErrorNote, StatusSelect, formatSalary, statusLabel, timeAgo } from './ui'

const ORDER = ['offer', 'interview', 'replied', 'applied', 'saved', 'rejected', 'skipped']

export default function Tracker({ onChanged }) {
  const [items, setItems] = useState([])
  const [error, setError] = useState(null)
  const [openId, setOpenId] = useState(null)
  const [showSkipped, setShowSkipped] = useState(false)

  const load = useCallback(() => api.tracker().then((d) => { setItems(d.items); setError(null) }).catch(setError), [])
  useEffect(() => { load() }, [load])

  const setStatus = async (id, status) => {
    try { await api.saveApplication(id, { status }); await load(); onChanged?.() } catch (e) { setError(e) }
  }

  const today = new Date().toISOString().slice(0, 10)
  const groups = ORDER.filter((s) => s !== 'skipped' || showSkipped)
    .map((s) => [s, items.filter((i) => i.application.status === s)])
    .filter(([, rows]) => rows.length)

  return (
    <div className="space-y-6">
      <ErrorNote error={error} />
      {items.length === 0 && (
        <p className="rounded-lg bg-white p-10 text-center text-slate-400 shadow-sm ring-1 ring-slate-200">
          Nothing tracked yet. Set a status on any posting in the Jobs screen and it appears here.
        </p>
      )}
      {groups.map(([status, rows]) => (
        <section key={status}>
          <h2 className="mb-2 text-sm font-semibold text-slate-700">{statusLabel(status)} <span className="font-normal text-slate-400">{rows.length}</span></h2>
          <div className="divide-y divide-slate-100 rounded-lg bg-white shadow-sm ring-1 ring-slate-200">
            {rows.map(({ job, application: a }) => (
              <div key={job.id} onClick={() => setOpenId(job.id)} className="flex cursor-pointer flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 hover:bg-indigo-50/40">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium text-slate-900">{job.title} <span className="font-normal text-slate-500">· {job.company}</span></p>
                  <p className="truncate text-xs text-slate-500">
                    {[job.location, formatSalary(job), a.applied_at && `applied ${timeAgo(a.applied_at)}`, a.contact_name && `contact: ${a.contact_name}`, a.notes].filter(Boolean).join(' · ')}
                  </p>
                </div>
                {job.closed && <Badge className="bg-slate-100 text-slate-500 ring-slate-200">posting closed</Badge>}
                {a.follow_up_on && (
                  <Badge className={a.follow_up_on <= today ? 'bg-amber-50 text-amber-800 ring-amber-200' : 'bg-slate-50 text-slate-600 ring-slate-200'}>
                    follow up {a.follow_up_on}
                  </Badge>
                )}
                <StatusSelect status={a.status} onChange={(s) => setStatus(job.id, s)} />
              </div>
            ))}
          </div>
        </section>
      ))}
      {items.some((i) => i.application.status === 'skipped') && (
        <label className="flex items-center gap-2 text-sm text-slate-500">
          <input type="checkbox" checked={showSkipped} onChange={(e) => setShowSkipped(e.target.checked)} className="rounded border-slate-300" />
          Show skipped postings
        </label>
      )}
      {openId && <JobDrawer id={openId} onClose={() => setOpenId(null)} onSaved={() => { load(); onChanged?.() }} />}
    </div>
  )
}
