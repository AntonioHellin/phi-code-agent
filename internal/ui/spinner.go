package ui

import (
	"fmt"
	"time"
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Spinner renders an animated CLI spinner in a background goroutine.
type Spinner struct {
	stop chan struct{}
	done chan struct{}
}

// StartSpinner begins the spinner animation with the given label.
func StartSpinner(label string) *Spinner {
	s := &Spinner{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K") // Erase spinner line
				return
			case <-ticker.C:
				fmt.Printf("\r%c %s", spinnerFrames[i], label)
				i = (i + 1) % len(spinnerFrames)
			}
		}
	}()
	return s
}

// Stop terminates the spinner and waits for line cleanup.
func (s *Spinner) Stop() {
	close(s.stop)
	<-s.done
}

