package main

import "time"

// Fires once immediately, then on interval (unlike time.Ticker).
type immediateTicker struct {
	ch     chan time.Time
	ticker *time.Ticker
	done   chan struct{}
}

func newImmediateTicker(d time.Duration) *immediateTicker {
	t := &immediateTicker{
		ch:     make(chan time.Time, 1),
		ticker: time.NewTicker(d),
		done:   make(chan struct{}),
	}
	t.ch <- time.Now()
	go func() {
		for {
			select {
			case <-t.done:
				return
			case now := <-t.ticker.C:
				select {
				case t.ch <- now:
				default:
				}
			}
		}
	}()
	return t
}

func (t *immediateTicker) C() <-chan time.Time { return t.ch }

func (t *immediateTicker) Stop() {
	t.ticker.Stop()
	close(t.done)
}
