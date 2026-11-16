// Thin wrapper over fetch for the Go api.
async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error || `${method} ${path} failed (${res.status})`)
  return data
}

const qs = (params) => {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== '' && v != null && v !== false) p.set(k, v === true ? '1' : v)
  return p.toString()
}

export const api = {
  stats: () => request('GET', '/api/stats'),
  jobs: (params) => request('GET', '/api/jobs?' + qs(params)),
  job: (id) => request('GET', `/api/jobs/${id}`),
  saveApplication: (id, patch) => request('PUT', `/api/jobs/${id}/application`, patch),
  tracker: () => request('GET', '/api/tracker'),
  settings: () => request('GET', '/api/settings'),
  saveSettings: (s) => request('PUT', '/api/settings', s),
  companies: (params = {}) => request('GET', '/api/companies?' + qs(params)),
  addCompany: (c) => request('POST', '/api/companies', c),
  patchCompany: (id, patch) => request('PATCH', `/api/companies/${id}`, patch),
  removeCompany: (id) => request('DELETE', `/api/companies/${id}`),
  crawlCompany: (id) => request('POST', `/api/companies/${id}/crawl`),
  deepSearch: (name) => request('POST', '/api/search', { name }),
  search: (id) => request('GET', `/api/search/${id}`),
  pickCandidate: (searchId, domain) => request('POST', `/api/search/${searchId}/pick`, { domain }),
  control: () => request('GET', '/api/control'),
  setControl: (patch) => request('PUT', '/api/control', patch),
  events: (params = {}) => request('GET', '/api/events?' + qs(params)),
}
