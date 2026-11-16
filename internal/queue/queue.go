// Package queue wraps NATS JetStream: connect, publish, consume, ack, dead-letter.
package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Queue struct {
	NC *nats.Conn
	JS jetstream.JetStream
}

// Connect dials NATS with retries (containers start before NATS is ready) and
// keeps reconnecting forever afterwards, so a NATS restart does not kill workers.
func Connect(ctx context.Context, url, clientName string) (*Queue, error) {
	var nc *nats.Conn
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		nc, err = nats.Connect(url,
			nats.Name(clientName),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("nats not reachable: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Queue{NC: nc, JS: js}, nil
}

func (q *Queue) Close() { q.NC.Close() }

// Ping reports whether the connection is up and JetStream answers.
func (q *Queue) Ping(ctx context.Context) error {
	if !q.NC.IsConnected() {
		return fmt.Errorf("nats disconnected")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := q.JS.AccountInfo(ctx)
	return err
}
