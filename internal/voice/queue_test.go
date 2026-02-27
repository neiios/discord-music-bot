package voice

import (
	"sync"
	"testing"

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
		if !ok {
			t.Fatalf("expected song %q, got empty queue", want)
		}
		if got.Metadata.Title != want {
			t.Errorf("got %q, want %q", got.Metadata.Title, want)
		}
	}

	_, ok := q.Pop()
	if ok {
		t.Error("expected empty queue after popping all songs")
	}
}

func TestAddReturnsPosition(t *testing.T) {
	q := NewQueue()

	if pos := q.Add(song("A")); pos != 1 {
		t.Errorf("first Add returned %d, want 1", pos)
	}
	if pos := q.Add(song("B")); pos != 2 {
		t.Errorf("second Add returned %d, want 2", pos)
	}

	q.Pop()

	if pos := q.Add(song("C")); pos != 2 {
		t.Errorf("Add after Pop returned %d, want 2", pos)
	}
}

func TestClear(t *testing.T) {
	q := NewQueue()

	q.Add(song("A"))
	q.Add(song("B"))
	q.Add(song("C"))

	n := q.Clear()
	if n != 3 {
		t.Errorf("Clear returned %d, want 3", n)
	}

	if q.Len() != 0 {
		t.Errorf("Len after Clear = %d, want 0", q.Len())
	}

	_, ok := q.Pop()
	if ok {
		t.Error("expected empty queue after Clear")
	}

	n = q.Clear()
	if n != 0 {
		t.Errorf("Clear on empty returned %d, want 0", n)
	}
}

func TestListSnapshotIndependence(t *testing.T) {
	q := NewQueue()

	q.Add(song("A"))
	q.Add(song("B"))

	snapshot := q.List()
	if len(snapshot) != 2 {
		t.Fatalf("List returned %d songs, want 2", len(snapshot))
	}

	snapshot[0] = song("X")

	list2 := q.List()
	if list2[0].Metadata.Title != "A" {
		t.Error("mutating List snapshot affected the queue")
	}
}

func TestLen(t *testing.T) {
	q := NewQueue()

	if q.Len() != 0 {
		t.Errorf("Len on empty = %d, want 0", q.Len())
	}

	q.Add(song("A"))
	q.Add(song("B"))

	if q.Len() != 2 {
		t.Errorf("Len = %d, want 2", q.Len())
	}

	q.Pop()

	if q.Len() != 1 {
		t.Errorf("Len after Pop = %d, want 1", q.Len())
	}
}

func TestSignalClosesOnAdd(t *testing.T) {
	q := NewQueue()

	sig := q.Signal()

	select {
	case <-sig:
		t.Fatal("signal closed before any Add")
	default:
	}

	q.Add(song("A"))

	select {
	case <-sig:
	default:
		t.Fatal("signal not closed after Add")
	}

	sig2 := q.Signal()
	select {
	case <-sig2:
		t.Fatal("new signal closed prematurely")
	default:
	}
}

func TestConsumedSignalClosesOnPop(t *testing.T) {
	q := NewQueue()
	q.Add(song("A"))

	consumed := q.Consumed()

	select {
	case <-consumed:
		t.Fatal("consumed closed before any Pop")
	default:
	}

	q.Pop()

	select {
	case <-consumed:
	default:
		t.Fatal("consumed not closed after Pop")
	}

	// New channel should be open.
	consumed2 := q.Consumed()
	select {
	case <-consumed2:
		t.Fatal("new consumed closed prematurely")
	default:
	}
}

func TestConsumedNotClosedOnAdd(t *testing.T) {
	q := NewQueue()

	consumed := q.Consumed()
	q.Add(song("A"))

	select {
	case <-consumed:
		t.Fatal("consumed closed after Add (should only close on Pop)")
	default:
	}
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

	if q.Len() != n {
		t.Errorf("Len = %d after %d concurrent adds, want %d", q.Len(), n, n)
	}

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
	if count != n {
		t.Errorf("popped %d songs, want %d", count, n)
	}
}
