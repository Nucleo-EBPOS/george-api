// Package ratelimit é um limitador em memória por chave (janela deslizante).
package ratelimit

import (
	"sync"
	"time"
)

// Limiter guarda carimbos por chave dentro da janela.
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
	last   time.Time
}

// New cria um limitador de `limit` eventos por `window`.
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, hits: map[string][]time.Time{}, last: time.Now()}
}

// Allow registra um evento e diz se ainda cabe na janela.
func (l *Limiter) Allow(key string) bool {
	return l.allowAt(key, time.Now())
}

func (l *Limiter) allowAt(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.last) > l.window {
		l.gc(now)
		l.last = now
	}
	cut := now.Add(-l.window)
	ts := l.hits[key]
	keep := ts[:0]
	for _, t := range ts {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.limit {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

func (l *Limiter) gc(now time.Time) {
	cut := now.Add(-l.window)
	for k, ts := range l.hits {
		n := 0
		for _, t := range ts {
			if t.After(cut) {
				n++
			}
		}
		if n == 0 {
			delete(l.hits, k)
		}
	}
}
