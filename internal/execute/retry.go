package execute

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
)

// maxRetriedAtStartup is the most recovered Plans that startup recovery may fail to read, age out or start and leave to
// the background retry. If more fail, storage is failing broadly, and the process exits at once so a new process
// recovers them all.
const maxRetriedAtStartup = 3

// minRestartDelay is the least time a process waits before exiting to retry startup recovery of a Plan it could not
// start, so a storage outage at startup does not become a tight crash loop.
const minRestartDelay = time.Minute

// retryPolicy paces the attempts to Resume a Plan that startup recovery could not start: 1s growing to 60s, with no
// limit on attempts. The attempts end when the Plan resumes or its restart time passes (see restartBy).
func retryPolicy() exponential.Policy {
	p := exponential.SecondsRetryPolicy()
	p.MaxAttempts = 0
	return p
}

// restartBy returns when a process that still cannot resume a Plan startup recovery failed to start should exit, so a
// new process runs startup recovery on it again. That is once half the maxAge limit has passed since the Plan's last
// update, which leaves a new process the other half before the Plan is too old to recover. It is never sooner than
// minRestartDelay from now.
func restartBy(lastUpdate time.Time, maxAge time.Duration, now time.Time) time.Time {
	at := lastUpdate.Add(maxAge / 2)
	if earliest := now.Add(minRestartDelay); at.Before(earliest) {
		return earliest
	}
	return at
}

// retryResume keeps trying to Resume a Plan that startup recovery could not start, so a storage failure at startup does
// not leave the Plan Running in storage with nothing running it. If the Plan is still not running at restartAt, the
// process exits so a new process runs startup recovery on it again. The attempts run on a Context derived from ctx
// that ctx ending does not cancel, as the runs themselves do: ctx is New's, and New's caller may bound initialization
// with a deadline that must not end the retries of a Plan left Running in storage.
func (e *Plans) retryResume(ctx context.Context, id uuid.UUID, restartAt time.Time) {
	ctx = context.WithoutCancel(ctx)
	task := func(ctx context.Context) error {
		return e.resumeOrExit(ctx, id, restartAt)
	}
	if err := context.Tasks(ctx).Run(ctx, "resumeRecovered", task, e.retryBoff); err != nil {
		// ctx cannot end, so Run only refuses the task when the Tasks are closed: the process is shutting down.
		context.Log(ctx).Error("coercion: could not retry resuming a recovered plan", "id", id, "error", err)
	}
}

// resumeOrExit makes one attempt to Resume a Plan that startup recovery could not start. It returns nil once the Plan
// is running or needs nothing more, and an error for the attempt to be retried otherwise. Once restartAt has passed it
// exits the process instead, unless a run here is executing the Plan or storage no longer records it as Running.
func (e *Plans) resumeOrExit(ctx context.Context, id uuid.UUID, restartAt time.Time) error {
	rctx, cancel := context.WithDeadline(ctx, restartAt)
	err := e.Resume(rctx, id)
	cancel()

	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return nil // Shutting down.
	case errors.IsNotFound(err):
		context.Log(ctx).Warn("coercion: recovered plan no longer exists, not resuming it", "id", id)
		return nil
	case errors.IsStorageInconsistent(err):
		// Neither retrying nor a restart can fix it; it needs repair.
		context.Log(ctx).Error("coercion: recovered plan's storage is inconsistent, not resuming it until it is repaired", "id", id, "error", err)
		return nil
	case e.now().Before(restartAt):
		// %v, not %w: a storage error that ran out of retries is marked permanent, which would end these attempts.
		return fmt.Errorf("resuming recovered plan(%s): %v", id, err)
	}

	c := e.stranded(ctx, id)
	if c == nil {
		return nil
	}
	// Exit holding the claim, so nothing can Resume or Start the Plan between the decision and the exit. exit only
	// returns in tests.
	defer c.release()
	context.Log(ctx).Error(
		"coercion: recovered plan could not be resumed in time, exiting so a new process recovers it",
		"id", id, "error", err,
	)
	e.exit(1)
	return nil
}

// stranded reports whether storage records a Plan as Running while no run in this process is executing it. If so, it
// returns the claim it took on the Plan, which the caller must release; otherwise it returns nil.
//
// A claim alone does not mean a run is executing: a recovery may still be setting up, or may have failed to start and
// not yet let the claim go. So stranded waits for a claim's run to report, and for a run that is not executing to let
// the claim go. It then takes the claim itself before reading storage and keeps it, as Start and Resume do, so a
// concurrent Resume cannot start the Plan between the read and the caller's decision to exit. A Plan that cannot be
// read counts as stranded: storage is still failing, and a new process retries it. A Plan whose storage is
// inconsistent does not: a new process could not read it either. If ctx ends, the process is
// shutting down and nothing is stranded.
func (e *Plans) stranded(ctx context.Context, id uuid.UUID) *claim {
	var c *claim
	for {
		var won bool
		// The run context is not used: no run is launched on this claim, and release cancels it.
		_, c, won = e.claimRun(ctx, id)
		if won {
			break
		}
		// A recovery's own start error does not matter here: it is not executing, so wait for it to let the claim go.
		executing, _ := c.executing(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case executing:
			return nil
		}
		// waiter is only closed, so anything but a close is a shutdown.
		if _, r := chans.Get(ctx, c.waiter); !r.Closed() {
			return nil
		}
	}

	plan, err := e.readStored(ctx, id)
	switch {
	case ctx.Err() != nil:
		c.release()
		return nil
	case err == nil && plan.State.Get().Status == workflow.Running:
		return c
	case err == nil, errors.IsNotFound(err), errors.IsStorageInconsistent(err):
		// Finished, gone, or damaged in a way a new process cannot fix.
		c.release()
		return nil
	}
	return c
}
