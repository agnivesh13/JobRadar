// Runs a deep search from the command line and prints its event stream,
// exactly what the Companies screen's panel shows.
//
//   node scripts/deep-search.mjs "Company name or website" [http://localhost:8080]

const [name, base = 'http://localhost:8080'] = process.argv.slice(2)
if (!name) {
  console.error('usage: node scripts/deep-search.mjs "<company>" [api base url]')
  process.exit(2)
}
const post = (path, body) => fetch(base + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }).then((r) => r.json())
const get = (path) => fetch(base + path).then((r) => r.json())
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const started = Date.now()
const search = await post('/api/search', { name })
if (search.error) { console.error(search.error); process.exit(1) }
const seen = new Set()
let state = search
for (;;) {
  await sleep(1500)
  state = await get(`/api/search/${search.search_id}`)
  const { events } = await get(`/api/events?search_id=${search.search_id}&limit=1000`)
  for (const e of events.reverse()) {
    if (seen.has(e.id)) continue
    seen.add(e.id)
    if (seen.size <= 40 || e.stage === 'score' || e.stage === 'match') {
      console.log(`${new Date(e.ts).toLocaleTimeString([], { hour12: false })} ${e.stage.padEnd(8)} ${e.outcome.padEnd(9)} ${e.detail || ''} ${e.url || ''}`.slice(0, 200))
    }
  }
  if (state.state === 'pick') {
    console.log('ambiguous; candidates:', state.candidates.map((c) => c.domain).join(', '))
    break
  }
  const done = events.find((e) => e.stage === 'score')
  if (done || state.state === 'failed' || Date.now() - started > 240000) {
    const jobs = events.filter((e) => e.stage === 'extract').reduce((n, e) => n + (e.n || 0), 0)
    const pages = events.filter((e) => e.stage === 'fetch' || e.stage === 'render').length
    console.log(`\n${state.state === 'failed' ? 'FAILED: ' + state.error : done ? 'FINISHED' : 'TIMED OUT'} in ${Math.round((Date.now() - started) / 1000)}s: ${pages} pages, ${jobs} jobs found, ${events.filter((e) => e.stage === 'match').length} match your filters`)
    break
  }
}
