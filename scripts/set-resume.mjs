// Loads a plain-text resume into JobRadar's settings (stored only in the
// local database) and triggers a re-score.
//
//   node scripts/set-resume.mjs path/to/resume.txt [http://localhost:8080]

import fs from 'node:fs'

const [file, base = 'http://localhost:8080'] = process.argv.slice(2).filter((a) => !a.startsWith('--'))
if (!file) {
  console.error('usage: node scripts/set-resume.mjs <resume.txt> [api base url]')
  process.exit(2)
}
const { settings, defaults } = await (await fetch(base + '/api/settings')).json()
// --reset-keywords also restores the built-in role keyword lists.
if (process.argv.includes('--reset-keywords')) Object.assign(settings, { include_keywords: defaults.include_keywords, exclude_keywords: defaults.exclude_keywords })
settings.resume_text = fs.readFileSync(file, 'utf8')
const res = await fetch(base + '/api/settings', {
  method: 'PUT',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(settings),
})
console.log(res.ok ? `resume saved (${settings.resume_text.length} characters); jobs are being re-scored` : `failed: ${res.status} ${await res.text()}`)
