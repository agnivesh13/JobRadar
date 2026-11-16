package score

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"time"

	"jobradar/internal/model"
	"jobradar/internal/pipeline"
	"jobradar/internal/salary"
	"jobradar/internal/store"
)

const settingsKey = "settings"

// Judge scores postings as they arrive and re-scores stored ones when the
// settings change. Settings and company signals are cached briefly so that
// scoring a 500-job feed does not run 500 extra queries.
type Judge struct {
	Store *store.Store
	Rates salary.Rates

	mu       sync.Mutex
	settings Settings
	signals  map[int64]store.CompanySignal
	loadedAt time.Time
}

const cacheFor = 20 * time.Second

func (j *Judge) load(ctx context.Context) (Settings, map[int64]store.CompanySignal, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.signals != nil && time.Since(j.loadedAt) < cacheFor {
		return j.settings, j.signals, nil
	}
	s, err := LoadSettings(ctx, j.Store)
	if err != nil {
		return s, nil, err
	}
	signals, err := j.Store.CompanySignals(ctx, int64(s.SalaryFloorLPA*100_000))
	if err != nil {
		return s, nil, err
	}
	j.settings, j.signals, j.loadedAt = s, signals, time.Now()
	return s, signals, nil
}

// Invalidate drops the cache, so the next call sees fresh settings.
func (j *Judge) Invalidate() {
	j.mu.Lock()
	j.signals = nil
	j.mu.Unlock()
}

// LoadSettings reads the saved settings, or the defaults when none are saved.
func LoadSettings(ctx context.Context, st *store.Store) (Settings, error) {
	raw, err := st.GetSetting(ctx, settingsKey)
	if err != nil {
		return Settings{}, err
	}
	s := Defaults()
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return Defaults(), nil // a corrupt document must not stop the pipeline
		}
	}
	return s.normalised(), nil
}

func SaveSettings(ctx context.Context, st *store.Store, s Settings) error {
	raw, err := json.Marshal(s.normalised())
	if err != nil {
		return err
	}
	return st.PutSetting(ctx, settingsKey, string(raw))
}

// Judge implements pipeline.Judge for a freshly extracted posting.
func (j *Judge) Judge(ctx context.Context, companyID int64, raw model.RawJob) (pipeline.Verdict, error) {
	s, signals, err := j.load(ctx)
	if err != nil {
		return pipeline.Verdict{}, err
	}
	v := pipeline.Verdict{Experience: Classify(raw.Title, raw.EmploymentType, raw.Description)}

	// Salary: the structured field first, then the text.
	var rng salary.Range
	var ok bool
	if raw.Pay != nil {
		rng, ok = salary.Normalize(*raw.Pay, j.Rates)
	}
	if !ok {
		rng, ok = salary.Parse(raw.Description, j.Rates)
	}
	if ok {
		v.SalaryMin, v.SalaryMax, v.SalaryText = &rng.Min, &rng.Max, rng.Text
		if len(v.SalaryText) > 300 {
			// Cut on a character boundary: Postgres rejects half a UTF-8 character.
			v.SalaryText = strings.ToValidUTF8(v.SalaryText[:300], "")
		}
	}

	sig := signals[companyID]
	res := Evaluate(Input{
		Title: raw.Title, Location: raw.Location, Remote: raw.Remote, Description: raw.Description,
		Experience: v.Experience, SalaryMin: v.SalaryMin, SalaryMax: v.SalaryMax,
	}, s, Signal{PayTier: sig.PayTier, FundingStage: sig.FundingStage, PaysAboveFloor: sig.PaysAboveFloor})
	v.SalaryState, v.Matched, v.Flags, v.FitScore, v.FitReason = res.SalaryState, res.Matched, res.Flags, res.FitScore, res.FitReason
	return v, nil
}

// RescoreAll re-applies the current settings to every open job. Experience
// and the parsed salary figures are kept; only what depends on the settings
// is recomputed.
func (j *Judge) RescoreAll(ctx context.Context) (int, error) {
	j.Invalidate()
	s, signals, err := j.load(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	var after int64
	for {
		jobs, err := j.Store.JobsForScoring(ctx, after, 500)
		if err != nil {
			return total, err
		}
		if len(jobs) == 0 {
			return total, nil
		}
		// Scoring is pure CPU (regular expressions over long descriptions), so
		// each batch is spread over all cores.
		updates := make([]store.ScoreUpdate, len(jobs))
		var wg sync.WaitGroup
		workers := max(runtime.NumCPU(), 1)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := w; i < len(jobs); i += workers {
					job := jobs[i]
					sig := signals[job.CompanyID]
					res := Evaluate(Input{
						Title: job.Title, Location: job.Location, Remote: job.Remote, Description: job.Description,
						Experience: job.Experience, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax,
					}, s, Signal{PayTier: sig.PayTier, FundingStage: sig.FundingStage, PaysAboveFloor: sig.PaysAboveFloor})
					updates[i] = store.ScoreUpdate{ID: job.ID, SalaryState: res.SalaryState, Matched: res.Matched,
						Flags: res.Flags, FitScore: res.FitScore, FitReason: res.FitReason}
				}
			}(w)
		}
		wg.Wait()
		if err := j.Store.ApplyScores(ctx, updates); err != nil {
			return total, err
		}
		total += len(jobs)
		after = jobs[len(jobs)-1].ID
	}
}
