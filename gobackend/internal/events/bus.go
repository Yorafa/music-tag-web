// Package events defines a Bus abstraction for cross-process pub/sub.
//
// Two implementations live here:
//
//   • RedisBus — production. Uses go-redis/v9 Pub/Sub against the same
//     REDIS_ADDR asynq uses. Survives worker ⇄ gateway binary boundary.
//
//   • NullBus — test capture. Records every Publish into an in-memory slice
//     and fans the payload out to registered subscribers (so tests can
//     observe the worker→gateway flow without a real Redis).
//
// The interface is deliberately tiny (Publish + Subscribe) so handlers
// stay decoupled from the underlying transport.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/redis/go-redis/v9"
)

// Bus is the cross-process event-bus abstraction.
type Bus interface {
	// Publish serializes payload as JSON and broadcasts it on `topic`.
	// On Redis this maps to PUBLISH; on NullBus it fans to subscribers and
	// records into `Snapshot`.
	Publish(ctx context.Context, topic string, payload any) error

	// Subscribe returns a channel of raw JSON payloads for `topic` plus a
	// cancel closure that releases the underlying subscription. Callers
	// MUST invoke the cancel during graceful shutdown.
	Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error)
}

// ErrNoBus is returned when a method needs a Bus but was given nil.
var ErrNoBus = errors.New("events: nil bus")

// ─── RedisBus (production) ─────────────────────────────────────────────────

// RedisBus implements Bus using go-redis/v9 Pub/Sub. The connection is
// shared with asynq (cfg.RedisAddr) to keep middleware consistent.
type RedisBus struct {
	rdb *redis.Client
}

// NewRedisBus constructs a Redis-backed bus. addr is REDIS_ADDR (host:port).
func NewRedisBus(addr string) *RedisBus {
	return &RedisBus{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

// NewRedisBusFromClient wraps an existing *redis.Client (useful when the
// caller already constructed go-redis with auth/TLS options).
func NewRedisBusFromClient(rdb *redis.Client) *RedisBus {
	return &RedisBus{rdb: rdb}
}

// Publish broadcasts payload as JSON to the topic channel.
func (b *RedisBus) Publish(ctx context.Context, topic string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return b.rdb.Publish(ctx, topic, data).Err()
}

// Subscribe returns a buffered channel plus a cancel closure. The cancel
// unsubscribes from Redis and drains the channel.
func (b *RedisBus) Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error) {
	sub := b.rdb.Subscribe(ctx, topic)
	out := make(chan []byte, 64)
	go func() {
		defer close(out)
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				_ = sub.Close()
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- []byte(msg.Payload):
				case <-ctx.Done():
					_ = sub.Close()
					return
				}
			}
		}
	}()
	cancel := func() { _ = sub.Close() }
	return out, cancel, nil
}

// Close releases the underlying Redis connection pool. Safe to call once
// after all subscribers have been shut down.
func (b *RedisBus) Close() error {
	if b.rdb != nil {
		return b.rdb.Close()
	}
	return nil
}

// ─── NullBus (test capture) ────────────────────────────────────────────────

// NullBus stores every Publish call so tests can inspect the resulting
// payload list with Snapshot. Subscribers receive a buffered channel that
// sees every subsequent publish (best-effort fan-out — drops if the channel
// is full to avoid test hangs).
type NullBus struct {
	mu        sync.Mutex
	published []Recorded
	subs      []chan []byte
}

// Recorded pairs a topic with its JSON-encoded payload.
type Recorded struct {
	Topic   string
	Payload []byte
}

// NewNullBus returns an in-memory bus suitable for tests.
func NewNullBus() *NullBus {
	return &NullBus{}
}

// Publish records the event and forwards to any subscribers.
func (b *NullBus) Publish(_ context.Context, topic string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.published = append(b.published, Recorded{Topic: topic, Payload: data})
	subs := append([]chan []byte{}, b.subs...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- data:
		default:
			// subscriber buffer full — drop to keep producer unblocked
		}
	}
	return nil
}

// Subscribe returns a channel + cancel that drains future Publish calls.
// The cancel is a no-op (no real subscription) but keeps the API identical
// to RedisBus for swappability.
func (b *NullBus) Subscribe(_ context.Context, _ string) (<-chan []byte, func(), error) {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch, func() {}, nil
}

// Snapshot returns an immutable copy of every Publish call recorded so far.
func (b *NullBus) Snapshot() []Recorded {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Recorded, len(b.published))
	copy(out, b.published)
	return out
}

// Reset clears the recording list and detaches all subscribers.
func (b *NullBus) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		close(ch)
	}
	b.published = nil
	b.subs = nil
}
