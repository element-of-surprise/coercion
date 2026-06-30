package execute

import (
	"testing"
)

func TestRunningClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		preclaim bool // a run for the id is already in flight
		wantWon  bool
	}{
		{
			name:    "Success: wins when the id is not already running",
			wantWon: true,
		},
		{
			name:     "Success: loses when a run for the id is already in flight",
			preclaim: true,
			wantWon:  false,
		},
	}

	for _, test := range tests {
		r := newRunning()
		id := NewV7()

		if test.preclaim {
			if _, won := r.claim(id, func() {}); !won {
				t.Errorf("TestRunningClaim(%s): setup claim did not win", test.name)
				continue
			}
		}

		cancelled := false
		release, won := r.claim(id, func() { cancelled = true })

		if won != test.wantWon {
			t.Errorf("TestRunningClaim(%s): got won == %v, want %v", test.name, won, test.wantWon)
			continue
		}

		if !test.wantWon {
			if release != nil {
				t.Errorf("TestRunningClaim(%s): got non-nil release on loss, want nil", test.name)
			}
			// On loss, claim must not touch state: the pre-existing single entry remains.
			if got := r.waiters.Len(); got != 1 {
				t.Errorf("TestRunningClaim(%s): got waiters.Len() == %d after loss, want 1", test.name, got)
			}
			if got := r.stoppers.Len(); got != 1 {
				t.Errorf("TestRunningClaim(%s): got stoppers.Len() == %d after loss, want 1", test.name, got)
			}
			continue
		}

		// On win, the id is observable via wait and the stopper is stored.
		w, ok := r.wait(id)
		if !ok || w == nil {
			t.Errorf("TestRunningClaim(%s): id not registered after win (ok=%v, nil=%v)", test.name, ok, w == nil)
			continue
		}
		if got := r.stoppers.Len(); got != 1 {
			t.Errorf("TestRunningClaim(%s): got stoppers.Len() == %d after win, want 1", test.name, got)
		}

		// release cancels, closes the waiter, and removes both entries.
		release()
		select {
		case <-w:
		default:
			t.Errorf("TestRunningClaim(%s): release did not close the waiter", test.name)
		}
		if !cancelled {
			t.Errorf("TestRunningClaim(%s): release did not invoke cancel", test.name)
		}
		if got := r.waiters.Len(); got != 0 {
			t.Errorf("TestRunningClaim(%s): got waiters.Len() == %d after release, want 0", test.name, got)
		}
		if got := r.stoppers.Len(); got != 0 {
			t.Errorf("TestRunningClaim(%s): got stoppers.Len() == %d after release, want 0", test.name, got)
		}
	}
}

// TestRunningReleaseOwnEntry verifies release deletes only the waiter it installed. A run claims,
// releases, then a second run claims the same id and installs a fresh waiter; the first run's
// CompareAndDelete must not remove the second's entry.
func TestRunningReleaseOwnEntry(t *testing.T) {
	t.Parallel()

	r := newRunning()
	id := NewV7()

	release1, won := r.claim(id, func() {})
	if !won {
		t.Fatalf("TestRunningReleaseOwnEntry: first claim did not win")
	}
	w1, _ := r.wait(id)
	release1()
	select {
	case <-w1:
	default:
		t.Fatalf("TestRunningReleaseOwnEntry: first release did not close its waiter")
	}

	// A second run for the same id now wins and installs a distinct waiter.
	release2, won := r.claim(id, func() {})
	if !won {
		t.Fatalf("TestRunningReleaseOwnEntry: second claim did not win after first released")
	}
	w2, ok := r.wait(id)
	if !ok || w2 == nil {
		t.Fatalf("TestRunningReleaseOwnEntry: second run not registered")
	}

	// The second run's entry must survive until its own release.
	release2()
	if got := r.waiters.Len(); got != 0 {
		t.Errorf("TestRunningReleaseOwnEntry: got waiters.Len() == %d after second release, want 0", got)
	}
}

func TestRunningWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claimed bool
		wantOK  bool
	}{
		{
			name:    "Success: hit when the id is currently claimed",
			claimed: true,
			wantOK:  true,
		},
		{
			name:   "Success: miss when the id was never claimed",
			wantOK: false,
		},
	}

	for _, test := range tests {
		r := newRunning()
		id := NewV7()

		if test.claimed {
			if _, won := r.claim(id, func() {}); !won {
				t.Errorf("TestRunningWait(%s): setup claim did not win", test.name)
				continue
			}
		}

		w, ok := r.wait(id)
		switch {
		case ok != test.wantOK:
			t.Errorf("TestRunningWait(%s): got ok == %v, want %v", test.name, ok, test.wantOK)
		case test.wantOK && w == nil:
			t.Errorf("TestRunningWait(%s): got nil channel on hit", test.name)
		case !test.wantOK && w != nil:
			t.Errorf("TestRunningWait(%s): got non-nil channel on miss, want nil", test.name)
		}
	}
}
