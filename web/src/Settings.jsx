import { useEffect, useState } from 'react'
import { api } from './api'
import { Button, ErrorNote, inputClass } from './ui'

const LEVELS = [
  ['intern', 'Internships'],
  ['new_grad', 'New graduate'],
  ['junior', '0 to 2 years'],
  ['mid', '3 to 5 years'],
  ['senior', 'Senior'],
]

const toList = (text) => text.split(/[,\n]/).map((s) => s.trim()).filter(Boolean)

export default function Settings({ onChanged }) {
  const [s, setS] = useState(null)
  const [meta, setMeta] = useState({})
  const [text, setText] = useState({ include: '', exclude: '', locations: '' })
  const [error, setError] = useState(null)
  const [state, setState] = useState('')

  const apply = (d) => {
    setS(d.settings)
    setMeta(d)
    setText({
      include: (d.settings.include_keywords || []).join(', '),
      exclude: (d.settings.exclude_keywords || []).join(', '),
      locations: (d.settings.locations || []).join(', '),
    })
  }
  useEffect(() => { api.settings().then(apply).catch(setError) }, [])

  if (!s) return <ErrorNote error={error} />

  const num = (k) => ({ type: 'number', value: s[k], onChange: (e) => setS({ ...s, [k]: Number(e.target.value) }), className: inputClass })
  const toggleLevel = (k) => setS({ ...s, experience: s.experience.includes(k) ? s.experience.filter((x) => x !== k) : [...s.experience, k] })

  const save = async (e) => {
    e.preventDefault()
    setState('saving')
    try {
      apply(await api.saveSettings({ ...s, include_keywords: toList(text.include), exclude_keywords: toList(text.exclude), locations: toList(text.locations) }))
      setError(null); setState('saved'); onChanged?.()
      setTimeout(() => setState(''), 4000)
    } catch (err) { setError(err); setState('') }
  }

  const Section = ({ title, hint, children }) => (
    <section className="rounded-lg bg-white p-5 shadow-sm ring-1 ring-slate-200">
      <h2 className="text-sm font-semibold text-slate-900">{title}</h2>
      {hint && <p className="mt-1 text-sm text-slate-500">{hint}</p>}
      <div className="mt-4 space-y-4">{children}</div>
    </section>
  )

  return (
    <form onSubmit={save} className="max-w-3xl space-y-5">
      <ErrorNote error={error} />

      {Section({ title: 'Salary', hint: 'A posting passes when the top of its stated range reaches the floor. Internships are compared against the monthly stipend floor instead.', children: (
        <>
          <div className="grid grid-cols-2 gap-4">
            <label className="text-sm text-slate-600">Salary floor (LPA)<input step="0.5" min="0" {...num('salary_floor_lpa')} /></label>
            <label className="text-sm text-slate-600">Internship stipend floor (₹ per month)<input step="1000" min="0" {...num('stipend_floor')} /></label>
          </div>
          <label className="flex items-center gap-2 text-sm text-slate-600">
            <input type="checkbox" checked={s.show_unknown} onChange={(e) => setS({ ...s, show_unknown: e.target.checked })} className="rounded border-slate-300" />
            Keep the “Salary unknown” tab (most Indian postings state no pay)
          </label>
        </>
      ) })}

      {Section({ title: 'Role', hint: 'Matched against the job title as whole words. Separate with commas.', children: (
        <>
          <label className="block text-sm text-slate-600">Title must contain one of
            <textarea rows={4} className={`${inputClass} mt-1`} value={text.include} onChange={(e) => setText({ ...text, include: e.target.value })} /></label>
          <label className="block text-sm text-slate-600">Title must not contain
            <textarea rows={3} className={`${inputClass} mt-1`} value={text.exclude} onChange={(e) => setText({ ...text, exclude: e.target.value })} /></label>
        </>
      ) })}

      {Section({ title: 'Experience and batch', children: (
        <>
          <div className="flex flex-wrap gap-x-5 gap-y-2">
            {LEVELS.map(([k, label]) => (
              <label key={k} className="flex items-center gap-2 text-sm text-slate-600">
                <input type="checkbox" checked={s.experience.includes(k)} onChange={() => toggleLevel(k)} className="rounded border-slate-300" />{label}
              </label>
            ))}
          </div>
          <div className="grid grid-cols-2 gap-4">
            <label className="text-sm text-slate-600">Graduation year<input min="2020" max="2040" {...num('grad_year')} /></label>
          </div>
          <label className="flex items-center gap-2 text-sm text-slate-600">
            <input type="checkbox" checked={s.hide_other_batch} onChange={(e) => setS({ ...s, hide_other_batch: e.target.checked })} className="rounded border-slate-300" />
            Hide postings that name a different graduation batch (otherwise they are only flagged)
          </label>
        </>
      ) })}

      {Section({ title: 'Location', hint: 'City names, “remote india”, or “anywhere”. Bangalore also matches Bengaluru.', children: (
        <input className={inputClass} value={text.locations} onChange={(e) => setText({ ...text, locations: e.target.value })} />
      ) })}

      {Section({ title: 'Resume', hint: 'Paste your resume text. The fit score counts how many of a posting’s skills appear here. It is stored only in your local database.', children: (
        <textarea rows={10} className={inputClass} value={s.resume_text} onChange={(e) => setS({ ...s, resume_text: e.target.value })} placeholder="Paste resume text" />
      ) })}

      {Section({ title: 'Optional extras', children: (
        <>
          <label className="flex items-center gap-2 text-sm text-slate-600">
            <input type="checkbox" checked={s.llm_enabled} disabled={!meta.llm_configured} onChange={(e) => setS({ ...s, llm_enabled: e.target.checked })} className="rounded border-slate-300" />
            LLM fit scoring {!meta.llm_configured && <span className="text-slate-400">(set LLM_BASE_URL in deploy/.env to enable)</span>}
          </label>
          <label className="flex items-center gap-2 text-sm text-slate-600">
            <input type="checkbox" checked={s.telegram_enabled} disabled={!meta.telegram_configured} onChange={(e) => setS({ ...s, telegram_enabled: e.target.checked })} className="rounded border-slate-300" />
            Telegram alert for new jobs scoring at least
            <input {...num('telegram_min_score')} min="0" max="100" className={`${inputClass} !w-20`} />
            {!meta.telegram_configured && <span className="text-slate-400">(set TELEGRAM_TOKEN to enable)</span>}
          </label>
        </>
      ) })}

      <div className="sticky bottom-0 flex items-center gap-3 bg-slate-50/90 py-3 backdrop-blur">
        <Button variant="primary" type="submit" disabled={state === 'saving'}>{state === 'saving' ? 'Saving…' : 'Save and re-score'}</Button>
        {state === 'saved' && <span className="text-sm text-emerald-600">Saved. Existing jobs are being re-scored.</span>}
      </div>
    </form>
  )
}
