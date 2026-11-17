# Learning journal

One file per build phase. Each one records what was built, why it was built that way, every problem hit along the way, and how it was solved. Read them in order.

| Phase | File | Status |
| --- | --- | --- |
| 0. Skeleton | [phase-0-skeleton.md](phase-0-skeleton.md) | Done |
| 1. First jobs | [phase-1-first-jobs.md](phase-1-first-jobs.md) | Done |
| 2. Usable tool | [phase-2-usable-tool.md](phase-2-usable-tool.md) | Done |
| 3. Distributed pipeline | [phase-3-distributed-pipeline.md](phase-3-distributed-pipeline.md) | Done: chaos test passed |
| 4. Live view | [phase-4-live-view.md](phase-4-live-view.md) | Done |
| 5. Whole-site crawling | [phase-5-whole-site-crawling.md](phase-5-whole-site-crawling.md) | Done, with known gaps listed in the file |
| 6. Discovery and scoring | [phase-6-discovery-and-scoring.md](phase-6-discovery-and-scoring.md) | Done; LLM and Telegram untested (no keys) |
| 7. Workday career sites (added after the plan) | [phase-7-workday.md](phase-7-workday.md) | Done; verified on two live sites |
| 8. Keka, Darwinbox, SmartRecruiters (added after the plan) | [phase-8-keka-darwinbox-smartrecruiters.md](phase-8-keka-darwinbox-smartrecruiters.md) | Keka and SmartRecruiters verified live; Darwinbox refuses the crawler |
| 9. Big companies: Amazon, Microsoft, Apple, Swiggy, Eternal (added after the plan) | [phase-9-big-companies.md](phase-9-big-companies.md) | Amazon, Microsoft, Apple, Swiggy crawled; Eternal hires by referral only; Google and Meta excluded by their robots.txt |

## How the phases were split

The split comes from PLAN.md. Each phase ends in something that runs and can be checked:

- **0** proves the plumbing (containers, database, queue, health checks) before any feature exists.
- **1** gets real data in with the simplest possible code path: one process, no queue.
- **2** makes the data useful: salary filter, screens, tracker.
- **3** takes the working single-process pipeline and splits it across a queue. Doing this *after* the logic works means queue bugs and logic bugs are never mixed.
- **4** adds visibility (live view) on top of the events the pipeline already emits.
- **5** and **6** widen coverage: any company site, then automatic discovery of new companies.

## How the work actually went

The phases were not built strictly one after another. The seed-list probe for Phase 1 ran in the background for over an hour, so Phase 2 and the start of Phase 3 were written while it ran, and Phases 4 to 6 were written while crawls and chaos runs were draining. Each phase's check was still run on its own and is recorded in its file. Because of that overlap, Phases 1 to 6 are one git commit rather than six.

## The ten lessons that cost the most time

1. A "skip if unchanged" shortcut must compare against work *completed*, not work *started* (Phase 3, problem 1).
2. "No" and "I could not check" are different answers; merging them turned a 5 second database outage into six permanently parked companies (Phase 3, problem 8).
3. Durable queues have fsync settings; the default is tuned for speed, not for kill -9 (Phase 3, problem 5).
4. Durability costs a disk flush, and batching is how many messages share one (Phase 3, problems 6 and the chunking note).
5. Slicing a Go string slices bytes; truncated web text needs strings.ToValidUTF8 (Phase 2, problem 7).
6. Classify errors as retryable or permanent where they are created (Phase 3, problem 3).
7. Read real output sorted by the extreme values; that is where parser bugs show (Phase 2, problems 1 to 4; Phase 6, problem 1).
8. When the compiler disagrees with memory, read the source of the version in go.mod (Phase 5, problem 1).
9. A silent zero looks like "nothing there"; always say why (Phase 5, problem 4).
10. Check the host port is free before choosing it, and read the first error, not the last (Phase 0, problem 2).
