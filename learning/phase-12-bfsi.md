# Phase 12: BFSI companies that hire freshers in India

**Not in PLAN.md.** Asked for after Phase 11: "list and add all BFSI sector companies that hire from India for freshers".
**Done when:** each company is crawled, or marked manual with a reason.

## Outcome (22 Nov 2026)

About 100 banking, financial-services and insurance companies with India offices or fresher programmes were looked up (the banks were Phase 11). 53 are crawled, 46 are manual.

| Group | Crawled | Manual (why) |
| --- | --- | --- |
| Indian fintech | CRED, Pine Labs, Upstox, Jupiter, Lendingkart, Fi, Dhan, Signzy, CoinSwitch, Perfios, plus earlier Paytm, Razorpay, PhonePe, Groww, Zerodha, Juspay, Navi, Slice, Zeta, Cashfree | MobiKwik, M2P, Go Digit, KreditBee (Darwinbox answers 403); BharatPe, Acko, Angel One, Jar, OneCard (script-built pages); Smallcase (PyjamaHR, no reader); PB Fintech, CoinDCX, INDmoney, Yubi, BillDesk (site answers 403) |
| NBFCs | Aditya Birla Capital, Tata Capital (Oracle) | Piramal (Darwinbox 403); Bajaj Finance, L&T Finance, Muthoot, Mahindra Finance, Edelweiss (403); Poonawalla, Shriram, HDB (script-built) |
| Indian insurers | Tata AIG (sitemap), Star Health | HDFC Life, ICICI Prudential, ICICI Lombard, Tata AIA (403); SBI Life, Axis Max Life, Bajaj Allianz (script-built) |
| Global insurers | Swiss Re, Munich Re, Chubb, MetLife, Travelers, The Hartford, Allstate, MassMutual, Sun Life, Manulife, Prudential, Gallagher, Marsh McLennan (Phenom) | Allianz (403); Aon (iCIMS sitemap forbidden); WTW (robots.txt disallows /jobs/); Liberty Mutual, Principal, Zurich (script-built) |
| Asset managers, data, ratings | Invesco, Franklin Templeton, Ameriprise, Morningstar, MSCI (iCIMS sitemap), Moody's, S&P Global, Experian, TransUnion, Equifax, Bloomberg | Fidelity International (403); CRISIL, FactSet (script-built) |
| Payments and fintech infrastructure | FIS, Fiserv, Global Payments (TSYS), Broadridge, SS&C, Western Union, Worldline (sitemap), Wise | Paysafe, Euronet (script-built) |
| Trading firms | NK Securities, Millennium (Eightfold), plus earlier Tower, Graviton, WorldQuant, IMC, D. E. Shaw | Arcesium (403 / 429); Quadeye, AlphaGrep (script-built) |

## Problems I hit and how I solved them

### 1. Name search again overloaded
- 34 of 95 searches failed at 4 at a time; by domain at 2 at a time, 13 more resolved. The rest were found by scanning careers pages for job-platform links, or by probing Workday tenants' robots.txt (which names every site: SS&C).

### 2. Wrong or noisy resolutions
- WTW resolved to an unrelated 404 page and Global Payments to payments.in; both were re-pointed or marked manual.
- Workday tenants brought many extra sites: Morningstar 27 (the "morningstar" site holds all 185 jobs; the rest are subsets of 0 to 2), Sun Life 8, Franklin Templeton 5. Subsets would show the same posting twice, so 39 extra sites were removed and their rows closed. Allstate's first site was its insurance-agent site; the careers site was kept.

### 3. More hosts on the Phase 11 readers
- **Sitemap reader:** Swiss Re, Tata AIG, Worldline (SuccessFactors) and MSCI (iCIMS, whose job pages carry JobPosting data).
- SuccessFactors pages carry schema.org microdata (`addressLocality`, `addressCountry`, `datePosted`). It is now the fallback for the location (Tata AIG has no location label) and the first choice for the date: "Thu Nov 12 15:00:00 UTC 2026" is unambiguous, while label dates are day first on some sites and month first on others (Worldline "10/19/26", which is not read).
- Country codes are spelled out in JobPosting data too (iCIMS "US", "IN"), so the location filter sees "India".
- **Eightfold older API:** Millennium. **Phenom:** Marsh McLennan, whose 1,906 jobs are worldwide; Phenom lists give each job's country, so other countries are skipped before their pages are fetched.

### 4. A fixture held a person's e-mail address
- Tata AIG's job page names a recruiter's address; it is replaced with recruiter@example.com in testdata.

## Known gaps

1. 46 companies are manual; most refuse the crawler (403) or build their job list with scripts.
2. "Hires freshers" is a property of each company's programmes, not something the crawler checks; the experience filter decides which postings show.

## How to verify

```
go test ./internal/boards/ -run "Sitemap|Phenom|Eightfold"
```
