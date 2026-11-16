import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import Live from './Live'
import Jobs from './Jobs'
import Tracker from './Tracker'
import Companies from './Companies'
import Settings from './Settings'

// Screens are added here as phases land.
const SCREENS = [
  ['live', 'Live', Live],
  ['jobs', 'Jobs', Jobs],
  ['tracker', 'Tracker', Tracker],
  ['companies', 'Companies', Companies],
  ['settings', 'Settings', Settings],
]

const current = () => {
  const key = window.location.hash.replace(/^#\/?/, '').split('?')[0]
  return SCREENS.some(([k]) => k === key) ? key : 'jobs'
}

export default function App() {
  const [screen, setScreen] = useState(current)
  const [stats, setStats] = useState(null)

  useEffect(() => {
    const onHash = () => setScreen(current())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  const refresh = useCallback(() => api.stats().then((d) => setStats(d.stats)).catch(() => {}), [])
  useEffect(() => { refresh(); const t = setInterval(refresh, 30000); return () => clearInterval(t) }, [refresh])

  const Screen = SCREENS.find(([k]) => k === screen)[2]

  return (
    <div className="min-h-screen bg-slate-50 text-slate-800">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center gap-x-8 gap-y-2 px-4 py-3">
          <a href="#/jobs" className="flex items-center gap-2 text-lg font-semibold text-slate-900">
            <span className="grid h-7 w-7 place-items-center rounded-md bg-indigo-600 text-sm text-white">JR</span>JobRadar
          </a>
          <nav className="flex gap-1">
            {SCREENS.map(([k, label]) => (
              <a key={k} href={`#/${k}`}
                className={`rounded-md px-3 py-1.5 text-sm font-medium ${screen === k ? 'bg-indigo-50 text-indigo-700' : 'text-slate-600 hover:bg-slate-100'}`}>
                {label}{k === 'tracker' && stats?.tracked ? <span className="ml-1.5 tabular-nums text-slate-400">{stats.tracked}</span> : null}
              </a>
            ))}
          </nav>
          {stats && (
            <p className="ml-auto text-sm text-slate-500">
              <span className="tabular-nums">{stats.companies}</span> companies · <span className="tabular-nums">{stats.open_jobs}</span> open postings · <span className="tabular-nums">{stats.matched}</span> match you
            </p>
          )}
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-4 py-6">
        <Screen onChanged={refresh} />
      </main>
    </div>
  )
}
