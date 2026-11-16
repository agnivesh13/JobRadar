# Learning journal

One file per build phase. Each one records what was built, why it was built that way, every problem hit along the way, and how it was solved. Read them in order.

| Phase | File | Status |
| --- | --- | --- |
| 0. Skeleton | [phase-0-skeleton.md](phase-0-skeleton.md) | Done |
| 1. First jobs | [phase-1-first-jobs.md](phase-1-first-jobs.md) | |
| 2. Usable tool | [phase-2-usable-tool.md](phase-2-usable-tool.md) | |
| 3. Distributed pipeline | [phase-3-distributed-pipeline.md](phase-3-distributed-pipeline.md) | |
| 4. Live view | [phase-4-live-view.md](phase-4-live-view.md) | |
| 5. Whole-site crawling | [phase-5-whole-site-crawling.md](phase-5-whole-site-crawling.md) | |
| 6. Discovery and scoring | [phase-6-discovery-and-scoring.md](phase-6-discovery-and-scoring.md) | |

## How the phases were split

The split comes from PLAN.md. Each phase ends in something that runs and can be checked:

- **0** proves the plumbing (containers, database, queue, health checks) before any feature exists.
- **1** gets real data in with the simplest possible code path: one process, no queue.
- **2** makes the data useful: salary filter, screens, tracker.
- **3** takes the working single-process pipeline and splits it across a queue. Doing this *after* the logic works means queue bugs and logic bugs are never mixed.
- **4** adds visibility (live view) on top of the events the pipeline already emits.
- **5** and **6** widen coverage: any company site, then automatic discovery of new companies.
