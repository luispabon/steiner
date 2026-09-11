package agent

import (
	"fmt"
	"sync"
	"testing"
)

func TestSteerQueueAddDrain(t *testing.T) {
	q := NewSteerQueue()
	q.Add(SteerMessage{Text: "one"})
	q.Add(SteerMessage{Text: "two"})

	got := q.Drain()
	if len(got) != 2 || got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("Drain() = %+v, want [one two]", got)
	}
	if got := q.Drain(); got != nil {
		t.Fatalf("Drain() after drain = %+v, want nil", got)
	}

	q.Add(SteerMessage{Text: "three"})
	if got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("drained slice clobbered by later Add: %+v", got)
	}
}

func TestSteerQueueZeroValue(t *testing.T) {
	var q SteerQueue
	q.Add(SteerMessage{Text: "one"})
	if q.Len() != 1 {
		t.Fatalf("Len() on zero-value queue after Add = %d, want 1", q.Len())
	}
	if got := q.Drain(); len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("Drain() on zero-value queue = %+v, want [one]", got)
	}
}

func TestSteerQueueTake(t *testing.T) {
	q := NewSteerQueue()
	q.Add(SteerMessage{Text: "one"})
	q.Add(SteerMessage{Text: "two"})

	got := q.Take()
	if len(got) != 2 || got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("Take() = %+v, want [one two]", got)
	}
	if got := q.Take(); got != nil {
		t.Fatalf("Take() after take = %+v, want nil", got)
	}
}

func TestSteerQueueSnapshot(t *testing.T) {
	q := NewSteerQueue()
	if got := q.Snapshot(); got != nil {
		t.Fatalf("Snapshot() on empty queue = %+v, want nil", got)
	}

	q.Add(SteerMessage{Text: "one"})
	snap := q.Snapshot()
	if len(snap) != 1 || snap[0].Text != "one" {
		t.Fatalf("Snapshot() = %+v, want [one]", snap)
	}
	if q.Len() != 1 {
		t.Fatalf("Len() after Snapshot() = %d, want 1", q.Len())
	}

	q.Add(SteerMessage{Text: "two"})
	if len(snap) != 1 || snap[0].Text != "one" {
		t.Fatalf("earlier snapshot mutated by later Add: %+v", snap)
	}
}

func TestSteerQueueClear(t *testing.T) {
	q := NewSteerQueue()
	q.Add(SteerMessage{Text: "one"})
	q.Clear()
	if q.Len() != 0 {
		t.Fatalf("Len() after Clear() = %d, want 0", q.Len())
	}
	if got := q.Drain(); got != nil {
		t.Fatalf("Drain() after Clear() = %+v, want nil", got)
	}
}

func TestSteerQueueLen(t *testing.T) {
	q := NewSteerQueue()
	if q.Len() != 0 {
		t.Fatalf("Len() on empty queue = %d, want 0", q.Len())
	}
	q.Add(SteerMessage{Text: "one"})
	q.Add(SteerMessage{Text: "two"})
	if q.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", q.Len())
	}
	q.Drain()
	if q.Len() != 0 {
		t.Fatalf("Len() after Drain() = %d, want 0", q.Len())
	}
}

func TestSteerQueueNilReceiver(t *testing.T) {
	var q *SteerQueue

	if got := q.Drain(); got != nil {
		t.Fatalf("nil.Drain() = %+v, want nil", got)
	}
	if got := q.Take(); got != nil {
		t.Fatalf("nil.Take() = %+v, want nil", got)
	}
	if got := q.Snapshot(); got != nil {
		t.Fatalf("nil.Snapshot() = %+v, want nil", got)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("nil.Len() = %d, want 0", got)
	}

	// Add and Clear on a nil receiver must not panic.
	q.Add(SteerMessage{Text: "one"})
	q.Clear()
}

func TestSteerQueueConcurrent(t *testing.T) {
	q := NewSteerQueue()

	const producers = 8
	const perProducer = 50

	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				q.Add(SteerMessage{Text: fmt.Sprintf("p%d-i%d", p, i)})
			}
		}(p)
	}

	seen := make(map[string]int)
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			for _, m := range q.Drain() {
				seen[m.Text]++
			}
			select {
			case <-stop:
				for _, m := range q.Drain() {
					seen[m.Text]++
				}
				return
			default:
			}
		}
	}()

	wg.Wait()
	close(stop)
	<-finished

	total := 0
	for _, c := range seen {
		total += c
	}
	if want := producers * perProducer; total != want {
		t.Fatalf("total observed messages = %d, want %d", total, want)
	}
	for msg, c := range seen {
		if c != 1 {
			t.Errorf("message %q observed %d times, want 1", msg, c)
		}
	}
}
