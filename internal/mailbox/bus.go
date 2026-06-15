package mailbox

import "sync"

// Bus is the in-process fan-out for new-mail / mutation events. SSE
// inbox subscribers pull from a channel that Bus delivers to. One
// process for v1 — no NATS — multi-process would need leader election
// on the poll loop too.
type Bus struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[chan struct{}]struct{}{}} }

// Subscribe returns a channel that fires when Broadcast is called.
// The channel is buffered (1) so a slow consumer doesn't block the
// producer; consecutive broadcasts coalesce into a single wake-up.
// Caller MUST call the returned cancel when done.
func (b *Bus) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}
	return ch, cancel
}

// Broadcast wakes every active subscriber. Non-blocking — coalesces if
// the subscriber hasn't drained its previous wake-up.
func (b *Bus) Broadcast() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
