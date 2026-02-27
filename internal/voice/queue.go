package voice

import (
	"slices"
	"sync"

	"github.com/neiios/discord-music-bot/internal/downloader"
)

type Queue struct {
	mu           sync.Mutex
	songs        []downloader.Song
	signal       chan struct{}
	consumed     chan struct{}
	popCount     int
	pendingTotal int
}

func NewQueue() *Queue {
	return &Queue{
		signal:   make(chan struct{}),
		consumed: make(chan struct{}),
	}
}

func (q *Queue) Add(song downloader.Song) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.songs = append(q.songs, song)
	pos := len(q.songs) + q.pendingTotal

	close(q.signal)
	q.signal = make(chan struct{})

	return pos
}

func (q *Queue) Pop() (downloader.Song, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.songs) == 0 {
		return downloader.Song{}, false
	}

	song := q.songs[0]
	q.songs = q.songs[1:]
	q.popCount++

	close(q.consumed)
	q.consumed = make(chan struct{})

	return song, true
}

func (q *Queue) Clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	n := len(q.songs)
	q.popCount += n
	q.songs = nil
	q.pendingTotal = 0
	return n
}

func (q *Queue) List() []downloader.Song {
	q.mu.Lock()
	defer q.mu.Unlock()

	snapshot := make([]downloader.Song, len(q.songs))
	copy(snapshot, q.songs)
	return snapshot
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.songs)
}

func (q *Queue) Signal() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.signal
}

func (q *Queue) Consumed() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.consumed
}

type QueueInserter struct {
	q            *Queue
	pos          int
	lastPopCount int
	pending      int
}

func (q *Queue) NewInserter(pending int) *QueueInserter {
	q.mu.Lock()
	defer q.mu.Unlock()

	ins := &QueueInserter{
		q:            q,
		pos:          len(q.songs) + q.pendingTotal,
		lastPopCount: q.popCount,
		pending:      pending,
	}
	q.pendingTotal += pending
	return ins
}

func (ins *QueueInserter) Add(song downloader.Song) int {
	q := ins.q
	q.mu.Lock()
	defer q.mu.Unlock()

	popDelta := q.popCount - ins.lastPopCount
	ins.pos -= popDelta
	ins.lastPopCount = q.popCount

	ins.pos = max(0, min(ins.pos, len(q.songs)))

	q.songs = slices.Insert(q.songs, ins.pos, song)
	ins.pos++

	if ins.pending > 0 {
		ins.pending--
		q.pendingTotal--
		if q.pendingTotal < 0 {
			q.pendingTotal = 0
		}
	}

	close(q.signal)
	q.signal = make(chan struct{})

	return ins.pos
}

func (ins *QueueInserter) Skip() {
	q := ins.q
	q.mu.Lock()
	defer q.mu.Unlock()

	if ins.pending > 0 {
		ins.pending--
		q.pendingTotal--
		if q.pendingTotal < 0 {
			q.pendingTotal = 0
		}
	}
}

func (ins *QueueInserter) Close() {
	q := ins.q
	q.mu.Lock()
	defer q.mu.Unlock()

	q.pendingTotal -= ins.pending
	if q.pendingTotal < 0 {
		q.pendingTotal = 0
	}
	ins.pending = 0
}
