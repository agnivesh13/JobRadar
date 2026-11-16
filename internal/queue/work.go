package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Subjects. One work-queue stream carries every stage; each subject has
// exactly one durable consumer, shared by all replicas of that service.
const (
	StreamCrawl = "CRAWL"
	StreamDLQ   = "DLQ"

	SubjFetchHigh   = "crawl.fetch.high"   // deep search and "crawl now"
	SubjFetchNormal = "crawl.fetch.normal" // background polling
	SubjRender      = "crawl.render"       // pages that need headless Chrome
	SubjFetched     = "crawl.fetched"      // fetcher -> extractor
	SubjScore       = "crawl.score"        // extractor -> scorer (jobs and crawl-done, in order)

	ConsFetchHigh   = "fetch-high"
	ConsFetchNormal = "fetch-normal"
	ConsRender      = "render"
	ConsExtract     = "extract"
	ConsScore       = "score"
)

const (
	// AckWait is the lease: a worker that stops heartbeating for this long
	// (it crashed, or was killed) loses the message, and it is redelivered.
	AckWait = 30 * time.Second
	// MaxAttempts failed tries before a message goes to the dead-letter stream.
	MaxAttempts = 5
	// DedupWindow: publishing the same message id twice within this window
	// stores it once. This makes "publish, then crash before ack" harmless.
	DedupWindow = 10 * time.Minute
)

var consumers = map[string]string{
	ConsFetchHigh:   SubjFetchHigh,
	ConsFetchNormal: SubjFetchNormal,
	ConsRender:      SubjRender,
	ConsExtract:     SubjFetched,
	ConsScore:       SubjScore,
}

// Setup creates the streams and consumers. It is idempotent, so every
// service calls it on start and whichever comes first does the work.
func (q *Queue) Setup(ctx context.Context) error {
	_, err := q.JS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       StreamCrawl,
		Subjects:   []string{"crawl.>"},
		Retention:  jetstream.WorkQueuePolicy, // a message is deleted once acknowledged
		Storage:    jetstream.FileStorage,     // survives a NATS restart
		Duplicates: DedupWindow,
		MaxAge:     7 * 24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("create stream %s: %w", StreamCrawl, err)
	}
	_, err = q.JS.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamDLQ,
		Subjects:  []string{"dlq.>"},
		Retention: jetstream.LimitsPolicy,
		Storage:   jetstream.FileStorage,
		MaxAge:    14 * 24 * time.Hour,
		MaxMsgs:   10_000,
	})
	if err != nil {
		return fmt.Errorf("create stream %s: %w", StreamDLQ, err)
	}
	for name, subject := range consumers {
		_, err := q.JS.CreateOrUpdateConsumer(ctx, StreamCrawl, jetstream.ConsumerConfig{
			Durable:       name,
			FilterSubject: subject,
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       AckWait,
			MaxDeliver:    MaxAttempts + 3, // server-side backstop; we dead-letter at MaxAttempts ourselves
			MaxAckPending: 1000,
		})
		if err != nil {
			return fmt.Errorf("create consumer %s: %w", name, err)
		}
	}
	return nil
}

// Publish stores one message. msgID makes the publish idempotent.
func (q *Queue) Publish(ctx context.Context, subject, msgID string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var opts []jetstream.PublishOpt
	if msgID != "" {
		opts = append(opts, jetstream.WithMsgID(msgID))
	}
	_, err = q.JS.Publish(ctx, subject, data, opts...)
	return err
}

// Item is one message of a batch.
type Item struct {
	Subject string
	MsgID   string
	Value   any
}

// PublishBatch stores many messages and returns once the server has confirmed
// every one. The publishes are pipelined instead of waiting for each
// confirmation in turn, which matters because the server flushes to disk on
// every write: one at a time costs a disk flush per message, a pipeline lets
// the server confirm them in groups. Order within the batch is preserved.
func (q *Queue) PublishBatch(ctx context.Context, items []Item) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	const chunk = 256
	for start := 0; start < len(items); start += chunk {
		end := min(start+chunk, len(items))
		futures := make([]jetstream.PubAckFuture, 0, end-start)
		for _, it := range items[start:end] {
			data, err := json.Marshal(it.Value)
			if err != nil {
				return err
			}
			f, err := q.JS.PublishAsync(it.Subject, data, jetstream.WithMsgID(it.MsgID))
			if err != nil {
				return err
			}
			futures = append(futures, f)
		}
		for _, f := range futures {
			select {
			case <-f.Ok():
			case err := <-f.Err():
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// Backlog is how much work a consumer still has: waiting plus in flight.
func (q *Queue) Backlog(ctx context.Context, consumer string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := q.JS.Consumer(ctx, StreamCrawl, consumer)
	if err != nil {
		return 0, err
	}
	info, err := c.Info(ctx)
	if err != nil {
		return 0, err
	}
	return int(info.NumPending) + info.NumAckPending, nil
}

// HasWorkers reports whether any process is currently pulling from a
// consumer (it has pull requests waiting on the server).
func (q *Queue) HasWorkers(ctx context.Context, consumer string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := q.JS.Consumer(ctx, StreamCrawl, consumer)
	if err != nil {
		return false
	}
	info, err := c.Info(ctx)
	return err == nil && info.NumWaiting > 0
}

// RetryAfter lets a handler ask for redelivery after a specific delay.
type RetryAfter struct {
	Delay time.Duration
	Err   error
}

func (e *RetryAfter) Error() string { return e.Err.Error() }
func (e *RetryAfter) Unwrap() error { return e.Err }

// Handler processes one message. nil acknowledges it; an error schedules a
// retry, and after MaxAttempts the message is dead-lettered.
type Handler func(ctx context.Context, data []byte, attempt int) error

type ConsumeOptions struct {
	// Consumers are polled in order: a message on an earlier consumer is
	// always taken before one on a later consumer (priority).
	Consumers   []string
	Concurrency int
	Log         *slog.Logger
	// OnDead runs after a message has been moved to the dead-letter stream.
	OnDead func(ctx context.Context, data []byte, cause error)
	// Paused, when set and returning true, stops new messages being taken.
	Paused func() bool
}

// Consume runs handler over the consumers until ctx is cancelled. On
// cancellation it stops taking messages and waits for in-flight handlers,
// so `docker compose stop` lets each worker finish its current task.
func (q *Queue) Consume(ctx context.Context, opts ConsumeOptions, handler Handler) error {
	cons := make([]jetstream.Consumer, len(opts.Consumers))
	for i, name := range opts.Consumers {
		c, err := q.JS.Consumer(ctx, StreamCrawl, name)
		if err != nil {
			return fmt.Errorf("consumer %s: %w", name, err)
		}
		cons[i] = c
	}
	var wg sync.WaitGroup
	for i := 0; i < max(opts.Concurrency, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if opts.Paused != nil && opts.Paused() {
					sleep(ctx, time.Second)
					continue
				}
				msg := q.next(ctx, cons)
				if msg == nil {
					continue
				}
				q.handle(msg, handler, opts)
			}
		}()
	}
	wg.Wait()
	return nil
}

// next takes one message, checking higher-priority consumers first.
func (q *Queue) next(ctx context.Context, cons []jetstream.Consumer) jetstream.Msg {
	for i, c := range cons {
		wait := 250 * time.Millisecond
		if i == len(cons)-1 {
			wait = 2 * time.Second // block a little on the last one instead of spinning
		}
		batch, err := c.Fetch(1, jetstream.FetchMaxWait(wait))
		if err != nil {
			sleep(ctx, time.Second) // NATS is down or restarting; the client reconnects by itself
			return nil
		}
		for msg := range batch.Messages() {
			return msg
		}
	}
	return nil
}

func (q *Queue) handle(msg jetstream.Msg, handler Handler, opts ConsumeOptions) {
	attempt := 1
	if meta, err := msg.Metadata(); err == nil {
		attempt = int(meta.NumDelivered)
	}
	// The handler context is not the service context: a shutdown signal must
	// not abort a task half way. It gets a generous deadline instead.
	hctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Heartbeat: extend the lease while the handler is still working.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(AckWait / 3)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				msg.InProgress()
			}
		}
	}()
	err := safely(func() error { return handler(hctx, msg.Data(), attempt) })
	close(stop)

	switch {
	case err == nil:
		// A plain ack does not wait for the server's confirmation. If it is lost,
		// the message is redelivered and the handler's idempotency covers that.
		if ackErr := msg.Ack(); ackErr != nil {
			opts.Log.Warn("ack failed; message will be redelivered", "subject", msg.Subject(), "err", ackErr)
		}
	case attempt >= MaxAttempts:
		opts.Log.Error("dead-lettering message", "subject", msg.Subject(), "attempts", attempt, "err", err)
		dead := nats.NewMsg("dlq." + msg.Subject())
		dead.Data = msg.Data()
		dead.Header.Set("X-Error", err.Error())
		dead.Header.Set("X-Attempts", fmt.Sprint(attempt))
		if _, pubErr := q.JS.PublishMsg(hctx, dead); pubErr != nil {
			msg.NakWithDelay(30 * time.Second) // could not dead-letter; keep the message
			return
		}
		if opts.OnDead != nil {
			opts.OnDead(hctx, msg.Data(), err)
		}
		msg.Term()
	default:
		delay := backoff(attempt)
		var ra *RetryAfter
		if errors.As(err, &ra) && ra.Delay > 0 {
			delay = ra.Delay
		}
		opts.Log.Warn("handler failed; will retry", "subject", msg.Subject(), "attempt", attempt, "retry_in", delay, "err", err)
		msg.NakWithDelay(delay)
	}
}

// safely turns a handler panic into an error, so one bad message cannot
// take the worker down.
func safely(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}

func backoff(attempt int) time.Duration {
	delays := []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}
	return delays[min(max(attempt, 1), len(delays))-1]
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
