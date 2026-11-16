// One-off seed builder: for each candidate slug, ask Greenhouse, Lever and
// Ashby whether a public job board exists under that name, and write the hits
// to seeds/companies.csv. One request per second per domain.
//
//   node scripts/probe-seeds.mjs            (takes about 10 minutes)
//
// Candidates are guesses (company name, lowercased). A miss only means the
// company is not on these three boards under that slug.

import fs from 'node:fs'

const UA = 'JobRadar/0.1 (personal job-search crawler)'

const candidates = `
razorpay razorpaysoftwareprivatelimited cred groww zepto swiggy meesho phonepe flipkart zomato olaelectric sharechat dream11
upstox zerodha slice jupiter epifi khatabook okcredit unacademy vedantu physicswallah cars24 spinny urbancompany
bigbasket blinkit licious lenskart nykaa mamaearth postman browserstack freshworks chargebee clevertap moengage
webengage hasura innovaccer druva icertis mindtickle whatfix leadsquared darwinbox zenoti yellowai gupshup haptik
uniphore observeai sprinklr highradius lentra perfios m2pfintech setu juspay cashfree payu pinelabs bharatpe paytm
mobikwik instamojo open niyo smallcase indmoney kuvera scripbox navi kreditbee moneyview fibe lendingkart acko
godigit policybazaar plum onsurity novabenefits pazcare healthifyme curefit cultfit practo pharmeasy tata1mg qureai
rapido bounce yulu atherenergy river ultraviolette blusmart porter delhivery shiprocket shadowfax locus loginext
fareye blackbuck zetwerk ofbusiness moglix inframarket udaan jumbotail dealshare ninjacart dehaat agrostar cropin
apna workindia instahyre scaler interviewbit newtonschool masaischool almabetter upgrad simplilearn greatlearning
eruditus emeritus leverageedu leapscholar teachmint classplus doubtnut cuemath pocketfm kukufm pratilipi dailyhunt
glance inmobi winzo mpl zupee gameskraft games24x7 nazara loco pixxel agnikul skyroot digantara ideaforge sarvam
sarvamai krutrim rephrase murf murfai peppercontent simplismart portkey composio neysa atlan hevodata sprinto scrut
zluri spendflo cloudeagle toplyne rocketlane facilio kissflow wingify exotel plivo msg91 wati interakt gallabox
bitespeed shopflo gokwik simpl snapmint uni onecard fplabs stashfin jar fello dezerv stablemoney wintwealth
gripinvest kredx vayana yubi progcap mintifi flexiloans indifi oxyzo zolve aspora vance skydo rupeek indiagold
cityflo zingbus ixigo cleartrip makemytrip oyo treebo stanzaliving nobroker housing squareyards livspace homelane
pepperfry wakefit furlenco rentomojo bluestone giva caratlane mokobara snitch bewakoof purplle sugarcosmetics
mcaffeine traya mosaicwellness bombayshavingcompany countrydelight rebelfoods chaayos thirdwavecoffee bluetokai
thewholetruth epigamia turtlemint coverfox ditto zeta zetasuite hyperverge idfy signzy digio leegality karza
bureau sardine threedots lambdatest testsigma katalon hackerrank hackerearth codingninjas geeksforgeeks unstop
internshala meta thoughtworks thoughtspot nutanix cohesity rubrik commvault netapp purestorage druva couchbase
stripe airbnb coinbase databricks confluent mongodb elastic gitlab hashicorp twilio okta cloudflare datadog
samsara toast doordash instacart pinterest lyft robinhood affirm brex chime plaid figma notion airtable asana
dropbox box zscaler crowdstrike sentinelone netskope harness cockroachlabs yugabyte singlestore clickhouse
starburst dremio fivetran dbtlabs airbyte hightouch amplitude mixpanel segment braze iterable klaviyo intercom
gong outreach salesloft apollo zoominfo 6sense demandbase automationanywhere celonis glean moveworks sumologic
newrelic pagerduty grafanalabs chronosphere honeycomb launchdarkly optimizely contentful sanity vercel netlify
render flyio railway supabase planetscale neon temporal temporaltechnologies retool zapier tines vanta drata
secureframe onetrust bigid wiz orca lacework snyk sonarsource circleci buildkite sourcegraph replit anysphere
openai anthropic cohere mistral perplexity perplexityai scaleai huggingface runwayml elevenlabs deepgram
assemblyai pinecone weaviate qdrant zilliz anyscale modal modallabs togetherai fireworksai baseten replicate
labelbox snorkelai wandb cresta sierra decagon harvey ramp mercury deel rippling gusto remote oyster lattice
cultureamp ashby gem checkr carta pilot navan expensify airwallex wise revolut monzo nubank grab gojek xendit
carousell ninjavan traveloka duolingo discord reddit roblox epicgames unity riotgames twitch spotify quora
medium substack coursera udemy khanacademy chegg quizlet grammarly calendly loom miro mural clickup linear
shortcut coda clari flexport motive nuro waymo aurora zoox appliedintuition anduril shieldai skydio
relativityspace spacex rocketlab planetlabs astranis palantir twosigma janestreet hudsonrivertrading drw optiver
imc jumptrading towerresearchcapital akunacapital susquehanna virtu fiverings oldmissioncapital quantbox
graviton gravitonresearchcapital alphagrep worldquant arcesium squarepoint point72 radixtrading headlandstech
citadel citadelsecurities imctrading qube quberesearch tibra davinci dvtrading maven flowtraders xtxmarkets
gsr wintermute cumberland b2c2 falconx coindcx coinswitch polygon polygontechnology aptoslabs solana
solanafoundation chainalysis alchemy consensys kraken gemini okx binance bitgo fireblocks anchorage ledger
uber atlassian canva shopify square block cashapp sofi upgrade marqeta adyen checkout checkoutcom klarna
gocardless tide zopa monese starlingbank n26 traderepublic bolt wolt glovo deliveryhero getir flink
gorillas picnic ocado deliveroo justeat zalando aboutyou hellofresh personio celonis contentsquare mirakl
doctolib alan qonto swile payfit spendesk pennylane backmarket vinted bolttech tokopedia bukalapak gotocompany
sea shopee lazada foodpanda agoda tiket kredivo akulaku funding fundingsocieties aspire volopay osome
sleek airasia circles circleslife trustedhealth trustana stashaway endowus syfe tigerbrokers moomoo futu
pagarme creditas ifood rappi kavak clip konfio bitso mercadolibre nuvemshop vtex wildlife quintoandar loft
eightfold eightfoldai phenom beamery hirevue hackajob turing andela crossover toptal braintrust deelhq
multiplier skuad rapyd payoneer melio tipalti papayaglobal monday wix fiverr lemonade riskified forter
appsflyer ironsource similarweb gongio walkme lightricks taboola outbrain mobileye wizio snykio
servicenow workday salesforce adobe intuit vmware broadcom qualcomm nvidia amd intel arm marvell
micron synopsys cadence mediatek nxp texasinstruments analogdevices siliconlabs rambus sifive tenstorrent
cerebras groq sambanova graphcore lightmatter ayarlabs psiquantum rigetti ionq quantinuum pasqal
tesla rivian lucid lucidmotors fisker canoo zoox cruise pony ponyai weride momenta wayve oxa einride
joby archer wisk lilium volocopter zipline wing matternet flytrex boom hermeus ursamajor firefly
impulse impulsespace varda vast axiomspace sierraspace blueorigin ula abl stoke stokespace
`.split(/\s+/).filter(Boolean)

// Pass a file of extra slugs (whitespace separated) to top up an existing CSV:
//   node scripts/probe-seeds.mjs more-slugs.txt
const extraFile = process.argv[2]
const existing = extraFile && fs.existsSync('seeds/companies.csv') ? fs.readFileSync('seeds/companies.csv', 'utf8').trim().split('\n').slice(1) : []
const known = new Set(existing.map((r) => r.split(',')[2]))
const slugs = [...new Set(extraFile ? fs.readFileSync(extraFile, 'utf8').split(/\s+/).filter(Boolean) : candidates)].filter((s) => !known.has(s))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function getJSON(url) {
  try {
    const res = await fetch(url, { headers: { 'User-Agent': UA }, signal: AbortSignal.timeout(30000) })
    if (res.status === 429) { await sleep(15000); return null }
    if (!res.ok) return null
    return await res.json()
  } catch { return null }
}

const boards = {
  greenhouse: async (s) => {
    const j = await getJSON(`https://boards-api.greenhouse.io/v1/boards/${s}/jobs`)
    return j?.jobs?.length ? { count: j.jobs.length, name: j.jobs[0].company_name } : null
  },
  lever: async (s) => {
    const j = await getJSON(`https://api.lever.co/v0/postings/${s}?mode=json`)
    return Array.isArray(j) && j.length ? { count: j.length } : null
  },
  ashby: async (s) => {
    const j = await getJSON(`https://api.ashbyhq.com/posting-api/job-board/${s}`)
    return j?.jobs?.length ? { count: j.jobs.length } : null
  },
}

const titleCase = (s) => s.replace(/(^|[^a-z0-9])([a-z])/g, (_, a, b) => a + b.toUpperCase())
const rows = [...existing]
for (const [i, slug] of slugs.entries()) {
  const started = Date.now()
  const results = await Promise.all(Object.entries(boards).map(async ([board, fn]) => [board, await fn(slug)]))
  // If a slug exists on several boards, keep the one with the most jobs.
  const best = results.filter(([, r]) => r).sort((a, b) => b[1].count - a[1].count)[0]
  if (best) {
    const name = (best[1].name || titleCase(slug)).replace(/,/g, ' ').replace(/\s+(job board|careers)$/i, '').trim()
    rows.push([name, best[0], slug].join(','))
    console.log(`${i + 1}/${slugs.length} HIT  ${slug} -> ${best[0]} (${best[1].count} jobs)`)
  } else if ((i + 1) % 25 === 0) console.log(`${i + 1}/${slugs.length} ...`)
  fs.writeFileSync('seeds/companies.csv', 'name,board_type,board_token\n' + rows.join('\n') + '\n')
  await sleep(Math.max(0, 1100 - (Date.now() - started)))
}
console.log(`done: ${rows.length} companies written to seeds/companies.csv`)
