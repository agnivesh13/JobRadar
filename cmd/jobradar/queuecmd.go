package main

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"jobradar/internal/config"
	"jobradar/internal/queue"
)

func init() { commands["queue"] = runQueue }

// runQueue prints the state of every stream and consumer: how many messages
// sit on each subject, and where each consumer stands. `queue -dead` also
// prints the dead letters with the error that put them there. `queue -reset`
// wipes the work queue (safe: Postgres holds all state).
func runQueue(ctx context.Context, cfg config.Config, args []string) error {
	q, err := queue.Connect(ctx, cfg.NatsURL, "jobradar-queue")
	if err != nil {
		return err
	}
	defer q.Close()
	if len(args) > 0 && args[0] == "-reset" {
		// Postgres holds all state, so the queue can be thrown away: crawls that
		// were in flight are scheduled again when their timeout passes.
		if err := q.JS.DeleteStream(ctx, queue.StreamCrawl); err != nil {
			return err
		}
		if err := q.Setup(ctx); err != nil {
			return err
		}
		fmt.Println("work queue wiped and recreated")
	}
	for _, name := range []string{queue.StreamCrawl, queue.StreamDLQ} {
		s, err := q.JS.Stream(ctx, name)
		if err != nil {
			fmt.Printf("%s: %v\n", name, err)
			continue
		}
		info, err := s.Info(ctx, jetstream.WithSubjectFilter(">"))
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d messages (seq %d..%d)\n", name, info.State.Msgs, info.State.FirstSeq, info.State.LastSeq)
		for subj, n := range info.State.Subjects {
			fmt.Printf("  subject %-22s %d\n", subj, n)
		}
		lister := s.ListConsumers(ctx)
		for c := range lister.Info() {
			fmt.Printf("  consumer %-13s waiting=%d in_flight=%d redelivered=%d delivered_seq=%d ack_floor_seq=%d pullers=%d\n",
				c.Name, c.NumPending, c.NumAckPending, c.NumRedelivered, c.Delivered.Stream, c.AckFloor.Stream, c.NumWaiting)
		}
	}
	if len(args) > 0 && args[0] == "-dead" {
		s, err := q.JS.Stream(ctx, queue.StreamDLQ)
		if err != nil {
			return err
		}
		info, _ := s.Info(ctx)
		for seq := info.State.FirstSeq; seq <= info.State.LastSeq && info.State.Msgs > 0; seq++ {
			m, err := s.GetMsg(ctx, seq)
			if err != nil {
				continue
			}
			fmt.Printf("dead #%d %s after %s attempts: %s\n    %.300s\n", seq, m.Subject, m.Header.Get("X-Attempts"), m.Header.Get("X-Error"), m.Data)
		}
	}
	return nil
}
