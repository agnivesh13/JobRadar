// Small shared pieces: formatting helpers, badges, the status picker.

export const STATUSES = [
  ['not_applied', 'Not applied'],
  ['saved', 'Saved'],
  ['applied', 'Applied'],
  ['replied', 'Replied'],
  ['interview', 'Interview'],
  ['offer', 'Offer'],
  ['rejected', 'Rejected'],
  ['skipped', 'Skipped'],
]
export const statusLabel = (s) => (STATUSES.find(([k]) => k === s) || [s, s])[1]

const statusStyle = {
  not_applied: 'bg-slate-100 text-slate-600 ring-slate-200',
  saved: 'bg-sky-50 text-sky-700 ring-sky-200',
  applied: 'bg-indigo-50 text-indigo-700 ring-indigo-200',
  replied: 'bg-violet-50 text-violet-700 ring-violet-200',
  interview: 'bg-amber-50 text-amber-800 ring-amber-200',
  offer: 'bg-emerald-50 text-emerald-700 ring-emerald-200',
  rejected: 'bg-rose-50 text-rose-700 ring-rose-200',
  skipped: 'bg-slate-50 text-slate-400 ring-slate-200',
}

export function Badge({ className = '', children, title }) {
  return (
    <span title={title} className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${className}`}>
      {children}
    </span>
  )
}

export function StatusBadge({ status }) {
  return <Badge className={statusStyle[status] || statusStyle.not_applied}>{statusLabel(status)}</Badge>
}

// A select styled as the status badge, so the status is visible and editable in one control.
export function StatusSelect({ status, onChange }) {
  return (
    <select
      value={status}
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => onChange(e.target.value)}
      className={`cursor-pointer rounded-full border-0 py-0.5 pl-2 pr-6 text-xs font-medium ring-1 ring-inset focus:ring-2 focus:ring-indigo-500 ${statusStyle[status] || statusStyle.not_applied}`}
    >
      {STATUSES.map(([k, label]) => (
        <option key={k} value={k}>{label}</option>
      ))}
    </select>
  )
}

const lakh = 100000
const money = (v) => (v >= 100 * lakh ? `₹${+(v / (100 * lakh)).toFixed(2)} Cr` : `₹${+(v / lakh).toFixed(1)} L`)

// Yearly rupees -> "₹12–18 L", or a monthly stipend for internships.
export function formatSalary(job) {
  if (!job.salary_max) return null
  if (job.experience === 'intern') {
    const m = (v) => `₹${Math.round(v / 12 / 1000)}k`
    return (job.salary_min && job.salary_min !== job.salary_max ? `${m(job.salary_min)}–${m(job.salary_max)}` : m(job.salary_max)) + '/mo'
  }
  if (job.salary_min && job.salary_min !== job.salary_max) return `${money(job.salary_min).replace(/ (L|Cr)$/, '')}–${money(job.salary_max).slice(1)}`
  return money(job.salary_max)
}

export function timeAgo(iso) {
  if (!iso) return ''
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 90) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  if (s < 86400 * 60) return `${Math.round(s / 86400)} d ago`
  return new Date(iso).toLocaleDateString()
}

export const LEVELS = { intern: 'Intern', new_grad: 'New grad', junior: '0–2 yrs', mid: '3–5 yrs', senior: 'Senior', '': '—' }

export function FitScore({ score, reason }) {
  const tone = score >= 75 ? 'text-emerald-700 bg-emerald-50 ring-emerald-200' : score >= 50 ? 'text-amber-800 bg-amber-50 ring-amber-200' : 'text-slate-500 bg-slate-50 ring-slate-200'
  return <Badge className={`tabular-nums ${tone}`} title={reason}>{score}</Badge>
}

export function Button({ variant = 'default', className = '', ...props }) {
  const styles = {
    default: 'bg-white text-slate-700 ring-1 ring-inset ring-slate-300 hover:bg-slate-50',
    primary: 'bg-indigo-600 text-white hover:bg-indigo-500',
    danger: 'bg-white text-rose-600 ring-1 ring-inset ring-rose-200 hover:bg-rose-50',
  }
  return <button {...props} className={`rounded-md px-3 py-1.5 text-sm font-medium shadow-sm disabled:opacity-50 ${styles[variant]} ${className}`} />
}

export const inputClass =
  'block w-full rounded-md border-0 bg-white px-2.5 py-1.5 text-sm text-slate-900 shadow-sm ring-1 ring-inset ring-slate-300 placeholder:text-slate-400 focus:ring-2 focus:ring-indigo-500'

export function ErrorNote({ error }) {
  if (!error) return null
  return <div className="rounded-md bg-rose-50 px-3 py-2 text-sm text-rose-700 ring-1 ring-rose-200">{String(error.message || error)}</div>
}
