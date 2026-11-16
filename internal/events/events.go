// Package events carries crawl events from every service to the api, which
// stores them in crawl_events and relays them to browsers over SSE.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	Stream    = "EVENTS"
	Subject   = "events.crawl"
	Heartbeat = "heartbeat" // core NATS only, never stored
	consumer  = "events-api"
)

// Stages.
const (
	StageSchedule = "schedule"
	StageFetch    = "fetch"
	StageRender   = "render"
	StageExtract  = "extract"
	StageScore    = "score"
	StageMatch    = "match"   // a new posting that passes the filters
	StageResolve  = "resolve" // deep search: name -> domain -> careers page -> board
	StageDiscover = "discover"
)

// Event is one line of the live feed and one row of crawl_events.
type Event struct {
	ID         int64     `json:"id,omitempty"`
	TS         time.Time `json:"ts"`
	Service    string    `json:"service"`
	CompanyID  int64     `json:"company_id,omitempty"`
	Company    string    `json:"company,omitempty"`
	URL        string    `json:"url,omitempty"`
	Stage      string    `json:"stage"`
	Outcome    string    `json:"outcome"` // ok | unchanged | retry | error | blocked | info ...
	Detail     string    `json:"detail,omitempty"`
	N          int       `json:"n,omitempty"` // a count: jobs extracted, jobs saved
	DurationMS int       `json:"duration_ms,omitempty"`
	SearchID   string    `json:"search_id,omitempty"`
}

// Beat is a worker saying "I am alive".
type Beat struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
	Busy     int    `json:"busy"` // tasks in flight
}

// Emitter publishes events. Emitting never blocks the pipeline and never
// fails it: the live view is a window, not a dependency.
type Emitter struct {
	NC      *nats.Conn
	Service string
	Log     *slog.Logger
}

func (e *Emitter) Emit(ev Event) {
	if e == nil || e.NC == nil {
		return
	}
	ev.Service = e.Service
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	if len(ev.Detail) > 500 {
		ev.Detail = strings.ToValidUTF8(ev.Detail[:500], "")
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	// Plain publish: the EVENTS stream captures the subject, and we do not wait for an ack.
	if err := e.NC.Publish(Subject, data); err != nil && e.Log != nil {
		e.Log.Debug("event not published", "err", err)
	}
}

// Beat announces this worker on the heartbeat subject.
func (e *Emitter) Beat(instance string, busy int) {
	if e == nil || e.NC == nil {
		return
	}
	data, _ := json.Marshal(Beat{Service: e.Service, Instance: instance, Busy: busy})
	e.NC.Publish(Heartbeat, data)
}

// Setup creates the stream that buffers events for the api. Events are kept
// for a day at most; the permanent copy is the crawl_events table.
func Setup(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      Stream,
		Subjects:  []string{Subject},
		Retention: jetstream.LimitsPolicy,
		Storage:   jetstream.FileStorage,
		MaxAge:    24 * time.Hour,
		MaxMsgs:   200_000,
		Discard:   jetstream.DiscardOld,
	})
	return err
}

// Consume delivers stored events to fn in batches, acknowledging a batch only
// after fn returns nil, so an api restart resumes exactly where it stopped.
func Consume(ctx context.Context, js jetstream.JetStream, fn func([]Event) error) error {
	cons, err := js.CreateOrUpdateConsumer(ctx, Stream, jetstream.ConsumerConfig{
		Durable:       consumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxAckPending: 2000,
	})
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		batch, err := cons.Fetch(200, jetstream.FetchMaxWait(500*time.Millisecond))
		if err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		var msgs []jetstream.Msg
		var evs []Event
		for m := range batch.Messages() {
			var ev Event
			if json.Unmarshal(m.Data(), &ev) == nil {
				evs = append(evs, ev)
			}
			msgs = append(msgs, m)
		}
		if len(msgs) == 0 {
			continue
		}
		if err := fn(evs); err != nil {
			for _, m := range msgs {
				m.NakWithDelay(2 * time.Second)
			}
			continue
		}
		for _, m := range msgs {
			m.Ack()
		}
	}
	return nil
}
