package voice

import (
	"sync"

	"github.com/neiios/discord-music-bot/internal/downloader"
)

type Queue struct {
	mu       sync.Mutex
	songs    []downloader.Song
	signal   chan struct{}
	consumed chan struct{}
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
	pos := len(q.songs)

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

	close(q.consumed)
	q.consumed = make(chan struct{})

	return song, true
}

func (q *Queue) Clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	n := len(q.songs)
	q.songs = nil
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
