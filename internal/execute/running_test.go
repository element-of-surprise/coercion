package execute

import (
	"fmt"
	"testing"

	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/workflow/context"
)

func TestClaim(t *testing.T) {
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

		var first *claim
		if test.preclaim {
			var won bool
			if first, won = r.claim(id, func() {}); !won {
				t.Errorf("TestClaim(%s): setup claim did not win", test.name)
				continue
			}
		}

		cancelled := false
		c, won := r.claim(id, func() { cancelled = true })

		if won != test.wantWon {
			t.Errorf("TestClaim(%s): got won == %v, want %v", test.name, won, test.wantWon)
			continue
		}

		if !test.wantWon {
			// On loss, claim hands back the claim in flight and must not touch state: the single entry remains.
			if c != first {
				t.Errorf("TestClaim(%s): got a claim other than the in-flight one on loss", test.name)
			}
			if got := r.claims.Len(); got != 1 {
				t.Errorf("TestClaim(%s): got claims.Len() == %d after loss, want 1", test.name, got)
			}
			continue
		}

		// On win, the id is observable via wait.
		w, ok := r.wait(id)
		if !ok || w == nil {
			t.Errorf("TestClaim(%s): id not registered after win (ok=%v, nil=%v)", test.name, ok, w == nil)
			continue
		}
		if got := r.claims.Len(); got != 1 {
			t.Errorf("TestClaim(%s): got claims.Len() == %d after win, want 1", test.name, got)
		}

		// release cancels, closes the waiter, decides the claim and removes the entry.
		c.release()
		if _, _, closed := chans.TryGet(w); !closed {
			t.Errorf("TestClaim(%s): release did not close the waiter", test.name)
		}
		if _, _, closed := chans.TryGet(c.decision.Done()); !closed {
			t.Errorf("TestClaim(%s): release did not decide the claim", test.name)
		}
		if !cancelled {
			t.Errorf("TestClaim(%s): release did not invoke cancel", test.name)
		}
		if got := r.claims.Len(); got != 0 {
			t.Errorf("TestClaim(%s): got claims.Len() == %d after release, want 0", test.name, got)
		}
	}
}

// TestRelease verifies release deletes only the claim it installed. A run claims,
// releases, then a second run claims the same id and installs a fresh claim; the first run's
// CompareAndDelete must not remove the second's entry.
func TestRelease(t *testing.T) {
	t.Parallel()

	r := newRunning()
	id := NewV7()

	c1, won := r.claim(id, func() {})
	if !won {
		t.Fatalf("TestRelease: first claim did not win")
	}
	w1, _ := r.wait(id)
	c1.release()
	if _, _, closed := chans.TryGet(w1); !closed {
		t.Fatalf("TestRelease: first release did not close its waiter")
	}

	// A second run for the same id now wins and installs a distinct claim.
	c2, won := r.claim(id, func() {})
	if !won {
		t.Fatalf("TestRelease: second claim did not win after first released")
	}
	w2, ok := r.wait(id)
	if !ok || w2 == nil {
		t.Fatalf("TestRelease: second run not registered")
	}
	// A repeated release of the first claim must not remove the second's entry.
	r.claims.CompareAndDelete(id, c1)
	if _, ok := r.wait(id); !ok {
		t.Fatalf("TestRelease: first claim's delete removed the second claim")
	}

	// The second run's entry must survive until its own release.
	c2.release()
	if got := r.claims.Len(); got != 0 {
		t.Errorf("TestRelease: got claims.Len() == %d after second release, want 0", got)
	}
}

// TestAwait covers what a caller that lost a claim learns from it. A claim's ownership and decision are one
// value, so a loser is never told "not claimed" while the holder is still deciding, always sees the holder's release
// as "no run, claim again" with the id free to claim, and is never decided by a claim it did not lose to.
func TestAwait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// decide acts on the lost claim (a) after b has lost to it, and may take further claims on r. An error fails the
		// row's setup.
		decide func(r *running, a *claim) error
		// cancelCtx ends the loser's context before it waits, so an undecided claim must report the context error. It
		// is how a row observes that a claim is still undecided, not an input of its own: decide is.
		cancelCtx bool
		// wantClaimable is whether the loser must win a claim on the id afterwards.
		wantClaimable bool
		wantLaunched  bool
		wantErr       bool
	}{
		{
			name:         "Success: a claim that became a run reports launched",
			decide:       func(_ *running, a *claim) error { a.launched(nil); return nil },
			wantLaunched: true,
		},
		{
			name: "Success: a claim that became a run and then ended still reports launched",
			decide: func(_ *running, a *claim) error {
				a.launched(nil)
				a.release()
				return nil
			},
			wantLaunched:  true,
			wantClaimable: true,
		},
		{
			name:          "Success: a claim released without a run reports not launched and the id is free to claim",
			decide:        func(_ *running, a *claim) error { a.release(); return nil },
			wantClaimable: true,
		},
		{
			name:      "Error: an undecided claim keeps the loser waiting until its context ends",
			decide:    func(_ *running, _ *claim) error { return nil },
			cancelCtx: true,
			wantErr:   true,
		},
		{
			name: "Error: a late launched on a released claim does not decide the next claim on the id",
			decide: func(r *running, a *claim) error {
				a.release()
				// The id is claimed again, and only then does the old holder's launch report, as it does when the run
				// ended before launch recorded it.
				if _, won := r.claim(a.id, func() {}); !won {
					return fmt.Errorf("next claim did not win")
				}
				a.launched(nil)
				return nil
			},
			cancelCtx: true,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		r := newRunning()
		id := NewV7()
		a, won := r.claim(id, func() {})
		if !won {
			t.Errorf("TestAwait(%s): setup claim did not win", test.name)
			continue
		}
		lost, won := r.claim(id, func() {})
		if won {
			t.Errorf("TestAwait(%s): second claim won, want it to lose", test.name)
			continue
		}

		if err := test.decide(r, a); err != nil {
			t.Errorf("TestAwait(%s): decide: %s", test.name, err)
			continue
		}

		ctx := t.Context()
		if test.cancelCtx {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		d, err := lost.await(ctx)
		if err == nil && !d.launched && test.cancelCtx {
			// The claim the loser lost to was released, so it claims again, as claimDecided does. In the late-launched
			// case it loses to the next holder and waits on that claim, which must still be undecided.
			next, won := r.claim(id, func() {})
			if won {
				t.Errorf("TestAwait(%s): retry won, want it to lose to the next holder", test.name)
				continue
			}
			d, err = next.await(ctx)
		}
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestAwait(%s): got err == nil, want err != nil", test.name)
			continue
		case err != nil && !test.wantErr:
			t.Errorf("TestAwait(%s): got err == %s, want err == nil", test.name, err)
			continue
		case err != nil:
			continue
		}
		if d.launched != test.wantLaunched {
			t.Errorf("TestAwait(%s): got launched == %v, want %v", test.name, d.launched, test.wantLaunched)
		}
		if test.wantClaimable {
			if _, won := r.claim(id, func() {}); !won {
				t.Errorf("TestAwait(%s): claim after the holder released did not win", test.name)
			}
		}
	}
}

// TestWaiter tests running.wait. It is named for the waiter channel wait returns, since TestWait tests Plans.Wait.
func TestWaiter(t *testing.T) {
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
				t.Errorf("TestWaiter(%s): setup claim did not win", test.name)
				continue
			}
		}

		w, ok := r.wait(id)
		switch {
		case ok != test.wantOK:
			t.Errorf("TestWaiter(%s): got ok == %v, want %v", test.name, ok, test.wantOK)
		case test.wantOK && w == nil:
			t.Errorf("TestWaiter(%s): got nil channel on hit", test.name)
		case !test.wantOK && w != nil:
			t.Errorf("TestWaiter(%s): got non-nil channel on miss, want nil", test.name)
		}
	}
}
