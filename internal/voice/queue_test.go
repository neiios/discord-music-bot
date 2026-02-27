package voice

import (
	"sync"
	"testing"

	"github.com/neiios/discord-music-bot/internal/assert"
	"github.com/neiios/discord-music-bot/internal/downloader"
)

func song(title string) downloader.Song {
	return downloader.Song{Metadata: downloader.Metadata{Title: title}}
}

func TestAddPopFIFO(t *testing.T) {
	q := NewQueue()

	q.Add(song("A"))
	q.Add(song("B"))
	q.Add(song("C"))

	titles := []string{"A", "B", "C"}
	for _, want := range titles {
		got, ok := q.Pop()
		assert.True(t, ok, "expected song %q, got empty queue", want)
		assert.Equal(t, got.Metadata.Title, want)
	}

	_, ok := q.Pop()
	assert.False(t, ok, "expected empty queue after popping all songs")
}

func TestAddReturnsPosition(t *testing.T) {
	q := NewQueue()

	assert.Equal(t, q.Add(song("A")), 1, "first Add")
	assert.Equal(t, q.Add(song("B")), 2, "second Add")

	q.Pop()

	assert.Equal(t, q.Add(song("C")), 2, "Add after Pop")
}

func TestClear(t *testing.T) {
	q := NewQueue()

	q.Add(song("A"))
	q.Add(song("B"))
	q.Add(song("C"))

	assert.Equal(t, q.Clear(), 3)
	assert.Equal(t, q.Len(), 0)

	_, ok := q.Pop()
	assert.False(t, ok, "expected empty queue after Clear")

	assert.Equal(t, q.Clear(), 0)
}

func TestListSnapshotIndependence(t *testing.T) {
	q := NewQueue()

	q.Add(song("A"))
	q.Add(song("B"))

	snapshot := q.List()
	assert.Lenf(t, snapshot, 2)

	snapshot[0] = song("X")

	list2 := q.List()
	assert.Equal(t, list2[0].Metadata.Title, "A", "mutating List snapshot affected the queue")
}

func TestLen(t *testing.T) {
	q := NewQueue()

	assert.Equal(t, q.Len(), 0)

	q.Add(song("A"))
	q.Add(song("B"))

	assert.Equal(t, q.Len(), 2)

	q.Pop()

	assert.Equal(t, q.Len(), 1)
}

func TestSignalClosesOnAdd(t *testing.T) {
	q := NewQueue()

	sig := q.Signal()
	assert.ChanOpen(t, sig)

	q.Add(song("A"))
	assert.ChanClosed(t, sig)

	sig2 := q.Signal()
	assert.ChanOpen(t, sig2)
}

func TestConsumedSignalClosesOnPop(t *testing.T) {
	q := NewQueue()
	q.Add(song("A"))

	consumed := q.Consumed()
	assert.ChanOpen(t, consumed)

	q.Pop()
	assert.ChanClosed(t, consumed)

	// New channel should be open.
	consumed2 := q.Consumed()
	assert.ChanOpen(t, consumed2)
}

func TestConsumedNotClosedOnAdd(t *testing.T) {
	q := NewQueue()

	consumed := q.Consumed()
	q.Add(song("A"))

	assert.ChanOpen(t, consumed)
}

func TestInserterOrdering(t *testing.T) {
	q := NewQueue()

	ins := q.NewInserter(3)

	ins.Add(song("P1"))
	q.Add(song("S1"))
	ins.Add(song("P2"))
	ins.Add(song("P3"))
	ins.Close()

	expected := []string{"P1", "P2", "P3", "S1"}
	for _, want := range expected {
		got, ok := q.Pop()
		assert.True(t, ok, "expected song %q, got empty queue", want)
		assert.Equal(t, got.Metadata.Title, want)
	}
}

func TestInserterWithPops(t *testing.T) {
	q := NewQueue()

	q.Add(song("Existing"))
	ins := q.NewInserter(2)

	// Pop existing song while inserter is active.
	got, _ := q.Pop()
	assert.Equalf(t, got.Metadata.Title, "Existing")

	ins.Add(song("P1"))
	ins.Add(song("P2"))
	ins.Close()

	expected := []string{"P1", "P2"}
	for _, want := range expected {
		got, ok := q.Pop()
		assert.True(t, ok, "expected song %q, got empty queue", want)
		assert.Equal(t, got.Metadata.Title, want)
	}
}

func TestInserterSkipAndClose(t *testing.T) {
	q := NewQueue()

	ins := q.NewInserter(3)
	ins.Add(song("P1"))
	ins.Skip()  // One fails to download.
	ins.Close() // One remaining pending released.

	assert.Equal(t, q.Add(song("S1")), 2)

	expected := []string{"P1", "S1"}
	for _, want := range expected {
		got, ok := q.Pop()
		assert.True(t, ok, "expected song %q, got empty queue", want)
		assert.Equal(t, got.Metadata.Title, want)
	}
}

func TestInserterAfterClear(t *testing.T) {
	q := NewQueue()

	ins := q.NewInserter(5)
	ins.Add(song("P1"))

	q.Clear()

	// These should not panic or make pendingTotal negative.
	ins.Skip()
	ins.Close()

	assert.Equal(t, q.Add(song("S1")), 1)
}

func TestMultipleInserters(t *testing.T) {
	q := NewQueue()

	ins1 := q.NewInserter(2)
	ins2 := q.NewInserter(2)

	// Complete first inserter before second.
	ins1.Add(song("P1a"))
	ins1.Add(song("P1b"))
	ins1.Close()

	ins2.Add(song("P2a"))
	ins2.Add(song("P2b"))
	ins2.Close()

	q.Add(song("S1"))

	expected := []string{"P1a", "P1b", "P2a", "P2b", "S1"}
	for _, want := range expected {
		got, ok := q.Pop()
		assert.True(t, ok, "expected song %q, got empty queue", want)
		assert.Equal(t, got.Metadata.Title, want)
	}
}

func TestAddPositionWithPending(t *testing.T) {
	q := NewQueue()

	ins := q.NewInserter(5)

	assert.Equal(t, q.Add(song("S1")), 6)

	ins.Close()

	assert.Equal(t, q.Add(song("S2")), 2)
}

func TestConcurrentAccess(t *testing.T) {
	q := NewQueue()
	const n = 100
	var wg sync.WaitGroup

	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			q.Add(song("song"))
			_ = q.Len()
			_ = q.List()
			_ = q.Signal()
			_ = q.Consumed()
		}(i)
	}
	wg.Wait()

	assert.Equal(t, q.Len(), n)

	wg.Add(n)
	popped := make(chan bool, n)
	for range n {
		go func() {
			defer wg.Done()
			_, ok := q.Pop()
			popped <- ok
		}()
	}
	wg.Wait()
	close(popped)

	count := 0
	for ok := range popped {
		if ok {
			count++
		}
	}
	assert.Equal(t, count, n)
}
