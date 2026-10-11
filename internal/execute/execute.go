// Package executes validates Plan objects, checks Plugins can run in this environment (via Plugins.Init()) and
// allows execution of the Plan objects by starting a statemachine that runs a Plan to completion.
package execute

import (
	"fmt"
	"os"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/google/uuid"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/values/chans"
	"github.com/gostdlib/base/values/immutable"
)

// ErrNotFound is returned when an object is not found in storage. It is errors.NotFound: a comparable sentinel
// that every not-found error a Vault returns wraps (see errors.ErrNotFound), so errors.Is(err, ErrNotFound)
// and errors.IsNotFound(err) find it in all of them, and it is safe to compare with == or use in a switch.
var ErrNotFound = storage.ErrNotFound

// runner runs a Plan through the statemachine.
// In production this is the statemachine.Run function.
type runner func(name string, req statemachine.Request[sm.Data], options ...statemachine.Option) (statemachine.Request[sm.Data], error)

// validator validates a workflow.Object.
type validator func(walk.Item) error

// Plans handles execution of workflow.Plan instances for a Workstream.
type Plans struct {
	// registry is the registry of plugins that can be used to execute Plans.
	registry *registry.Register
	// store is the storage backend for the Plans.
	store storage.Vault

	// states is the statemachine that runs the Plans.
	states *sm.States

	// running tracks plan IDs executing in this process and guards against duplicate in-flight runs.
	running *running
	// waitReads shares one storage read of a Plan among the Waits that want it at the same time. Many callers often
	// wait on one Plan, and all of them go to storage the moment its run ends.
	waitReads sync.Flight[uuid.UUID, *workflow.Plan]

	// runner is the function that runs the statemachine.
	// In production this is the statemachine.Run function.
	runner runner

	// validators is a list of validators that are run on a Plan before it is started. It is set once by addValidators
	// and only read afterward.
	validators immutable.Slice[validator]

	// maxLastUpdate is the maximum amount of time that can pass between updates to a Plan before it is considered stale
	// and cannot be recovered or resumed: startup recovery and Resume (and so Wait) mark it Failed with
	// FRExceedRecovery. Default 30m.
	maxLastUpdate time.Duration
	// maxSubmitTime is the maximum amount of time that can pass between submission and start of a Plan.
	maxSubmit time.Duration
	// recovery is true if recovery is allowed.
	recovery bool

	// retryBoff paces the attempts to Resume a Plan that startup recovery could not start.
	retryBoff *exponential.Backoff
	// exit ends the process when a Plan startup recovery could not start is still not running by its restart time.
	// It is os.Exit outside of tests.
	exit func(code int)
}

// Option is an option for configuring a Plans via New.
type Option func(*Plans) error

// WithMaxLastUpdate sets the maximum amount of time that can pass between updates to a Plan. If a Plan has not been
// updated in this amount of time, it is considered stale and cannot be recovered or resumed: startup recovery and
// Resume (and so Wait) mark it Failed with FRExceedRecovery instead of running it. If this is not set, the default is
// 30 minutes.
func WithMaxLastUpdate(d time.Duration) Option {
	return func(p *Plans) error {
		p.maxLastUpdate = d
		return nil
	}
}

// WithMaxSubmit sets the maximum amount of time that can pass between submission and start of a Plan.
// If a Plan has not been started in this amount of time, it is considered stale and cannot be started.
// If this is not set, the default is 30 minutes.
func WithMaxSubmit(d time.Duration) Option {
	return func(p *Plans) error {
		p.maxSubmit = d
		return nil
	}
}

// WithNoRecovery disables recovery of Plans that are in a Running state.
func WithNoRecovery() Option {
	return func(p *Plans) error {
		p.recovery = false
		return nil
	}
}

// New creates a new Executor. This should only be created once.
func New(ctx context.Context, store storage.Vault, reg *registry.Register, options ...Option) (*Plans, error) {
	e := &Plans{
		registry:      reg,
		store:         store,
		running:       newRunning(),
		runner:        statemachine.Run[sm.Data],
		maxLastUpdate: 30 * time.Minute,
		maxSubmit:     30 * time.Minute,
		recovery:      true,
		retryBoff:     exponential.Must(exponential.New(exponential.WithPolicy(retryPolicy()))),
		exit:          os.Exit,
	}

	for _, o := range options {
		if err := o(e); err != nil {
			return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
		}
	}

	if err := e.initPlugins(ctx); err != nil {
		// A plugin's Init checks that this environment meets its preconditions, so a failure is the plugin's to report
		// rather than a bug in this package.
		return nil, errors.ErrPlugin(ctx, err)
	}

	var err error
	e.states, err = sm.New(ctx, store, e.registry, e.maxLastUpdate)
	if err != nil {
		// E returns an error that is already typed unchanged.
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
	}

	e.addValidators()

	if e.recovery {
		if err = e.recover(ctx); err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
		}
	}

	return e, nil
}

func (e *Plans) addValidators() {
	e.validators = immutable.NewSlice([]validator{
		e.validateID,
		e.validateState,
		e.validatePlan,
		e.validateAction,
	})
}

// initPlugins initializes all plugins in the registry to make sure they
// meet the preconditions for execution.
func (e *Plans) initPlugins(ctx context.Context) error {
	g := context.Pool(ctx).Group()
	for plugin := range e.registry.Plugins() {
		plugin := plugin
		g.Go(ctx, func(ctx context.Context) error {
			err := plugin.Init()
			if err != nil {
				return fmt.Errorf("plugin(%s) failed to initialize: %w", plugin.Name(), err)
			}
			return nil
		})
	}

	return g.Wait(ctx)
}

// Start starts a previously Submitted Plan by its ID. Cancelling the Context will not Stop execution.
// Please use Stop to stop execution of a Plan. If the plan has already been started, this will return nil.
func (e *Plans) Start(ctx context.Context, id uuid.UUID) error {
	runCtx, c, won, err := e.claimDecided(ctx, id)
	if err != nil || !won {
		return err
	}

	// The claim is released here unless launch takes it: launch releases it itself if it fails, and the run does once
	// it ends. Releasing twice panics, so no other path in Start releases it.
	launched := false
	defer func() {
		if !launched {
			c.release()
		}
	}()

	plan, err := e.readStored(ctx, id)
	if err != nil {
		return err
	}

	switch plan.State.Get().Status {
	case workflow.NotStarted:
		if err := e.validateStartState(plan); err != nil {
			return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan(%s) failed validation: %w: %w", id, err, errors.ErrPermanent))
		}
	default:
		return nil
	}

	state := plan.State.Get()
	state.Status = workflow.Running
	state.Start = e.now()
	plan.State.Set(state)
	if err := e.store.UpdatePlan(ctx, plan); err != nil {
		// E returns an error that is already typed unchanged.
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, err)
	}

	launched = true
	return e.launch(ctx, runCtx, c, plan, nil)
}

// recover recovers a Plan that is in a Running state in storage and restarts it from where it left off.
// This is used when the Executor starts up. A Plan whose recovery fails to start, such as when storage is failing, is
// retried in the background with Resume; if it is still not running by its restart time (see restartBy), the process
// exits so a new process recovers it. A Plan recovery cannot read or age out because storage is failing is retried the
// same way. If more than maxRetriedAtStartup Plans need retrying, the process exits at once.
func (e *Plans) recover(ctx context.Context) error {
	recovery := recover{
		maxAge: e.maxLastUpdate,
		store:  e.store,
	}

	// Get a list of all Plans that need to be recovered.
	req := statemachine.Request[recoverData]{Ctx: ctx, Next: recovery.start}
	var err error
	req, err = statemachine.Run[recoverData]("recover", req)
	if err != nil {
		return err
	}

	if len(req.Data.plans) == 0 && len(req.Data.unrecovered) == 0 {
		context.Log(ctx).Info("coercion: no plans to recover")
		return nil
	}

	// recoveryStarted is used to wait for all the recovered plans to start running.
	// runPlan starts its own goroutine and this is used to signal when the plan has started.
	recoveryStarted := make([]*sm.Started, 0, len(req.Data.plans))
	restartAt := make([]time.Time, 0, len(req.Data.plans))
	for _, plan := range req.Data.plans {
		context.Log(ctx).Info("coercion: recovered plan", "id", plan.ID, "status", plan.State.Get().Status)
		// Taken before the run changes the Plan: a new process judges the Plan's age by what is stored.
		restartAt = append(restartAt, restartBy(walk.LastUpdate(ctx, plan), e.maxLastUpdate, e.now()))
		started := sm.NewStarted()
		recoveryStarted = append(recoveryStarted, started)
		// A failure to start is reported through started and retried below, so the error here is not logged again.
		_ = e.runPlan(ctx, plan, started)
	}

	// Plans recovery could not read or age out, then plans that did not start, are retried in the background. The waits
	// are not ended by ctx: the runs are detached from it, and every run reports (see launch), so a deadline on New's
	// ctx that passes while recoveries are still setting up must not count them as having failed to start.
	retries := req.Data.unrecovered
	waitCtx := context.WithoutCancel(ctx)
	for i, started := range recoveryStarted {
		if err := started.Wait(waitCtx); err != nil {
			context.Log(ctx).Error("coercion: recovered plan did not start", "id", req.Data.plans[i].ID, "error", err)
			retries = append(retries, unrecovered{id: req.Data.plans[i].ID, restartAt: restartAt[i]})
		}
	}
	if len(retries) > maxRetriedAtStartup {
		// Storage is failing broadly rather than for a Plan or two; restart and recover everything again.
		context.Log(ctx).Error("coercion: too many recovered plans could not be recovered, exiting so a new process recovers them", "failed", len(retries))
		e.exit(1)
		return nil
	}
	for _, r := range retries {
		e.retryResume(ctx, r.id, r.restartAt)
	}

	return nil
}

// runPlan runs a recovered Plan through the statemachine. This is a non-blocking call. It is
// idempotent per plan ID: if a run for the same ID is already in flight in this process, this is a
// no-op. This prevents a duplicate run from overwriting the waiter and letting the first finisher
// delete the second's entry, which would leave storage Running with no waiter (the source of the
// "running state, but isn't in the waiters" bug in Wait). started always receives a report, either
// from the run or here if no run is started. It is only for startup recovery, where nothing else is
// running yet; Resume must claim before reading the Plan (see Resume).
func (e *Plans) runPlan(ctx context.Context, plan *workflow.Plan, started *sm.Started) error {
	runCtx, c, won := e.claimRun(ctx, plan.ID)
	if !won {
		// The in-flight run owns this Plan; there is nothing for this caller to wait on.
		if started != nil {
			started.Report(nil)
		}
		return nil
	}

	return e.launch(ctx, runCtx, c, plan, started)
}

// claimRun takes the single-run claim for plan id, fencing out duplicate runs. On success it returns the run context,
// the claim (whose release MUST be called exactly once when the run finishes; launch defers it) and won == true. On
// failure a claim for id is already in flight: runCtx is nil, c is that in-flight claim and won == false. The run
// context is derived from ctx but not cancelled by it; only release (via Stop, later) cancels the run. It carries the
// default pool, not ctx's: a run lasts as long as its Plan, and its sequences and groups submit to the run context's
// pool, so a Limited pool the caller attached would share its slots with the run and could starve it.
func (e *Plans) claimRun(ctx context.Context, id uuid.UUID) (runCtx context.Context, c *claim, won bool) {
	runCtx = context.WithoutCancel(ctx)
	runCtx = context.SetPool(runCtx, context.Pool(runCtx).Default())
	runCtx, cancel := context.WithCancel(runCtx)
	c, won = e.running.claim(id, cancel)
	if !won {
		cancel()
		return nil, c, false
	}
	return runCtx, c, true
}

// claimDecided takes the claim on id for Start or Resume. If another caller holds it, claimDecided waits until that
// caller has started a run or let the claim go: a run in flight returns won == false and no error (there is nothing to
// do), and a released claim is tried again. So a caller is never told a plan is starting when the holder of the claim
// read it, found nothing to run, and let it go. The wait is on the very claim that was lost, so neither the holder's
// bookkeeping order nor a later claim on the same id can make the loser misreport. If the run is a recovery, the loser
// also waits for its report and returns the error that kept it from starting, since a recovered run that failed to
// start still holds its claim until it ends.
func (e *Plans) claimDecided(ctx context.Context, id uuid.UUID) (runCtx context.Context, c *claim, won bool, err error) {
	for {
		runCtx, c, won := e.claimRun(ctx, id)
		if won {
			return runCtx, c, true, nil
		}
		executing, err := c.executing(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, nil, false, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("waiting for another caller's claim on plan(%s): %w", id, err))
		case err != nil:
			// A recovered run that could not start holds its claim until it ends. Report what it reported, so a failed
			// recovery is not taken for a run in flight and a caller retries it.
			return nil, nil, false, err
		case executing:
			return nil, nil, false, nil
		}
		// Released without a run: try again.
	}
}

// launch submits the statemachine run for plan on a goroutine and returns immediately. runCtx and c must come from a
// winning claimRun; the goroutine calls c.release exactly once on completion. The run is submitted on runCtx, not ctx,
// so a caller whose context ends does not stop the run from being scheduled, and the run itself uses only runCtx,
// which carries ctx's values but outlives it. If the pool refuses the work, the claim is released, started (if any)
// gets the error and an error is returned, so nothing waits on a run that never started. If the run ends before
// sm.Recovery reports, started gets an error, so a waiter never hangs or mistakes that for success.
func (e *Plans) launch(ctx, runCtx context.Context, c *claim, plan *workflow.Plan, started *sm.Started) error {
	// A run lasts as long as its Plan, so it goes on the default pool, never on a limited pool a caller's Context may
	// carry, where it would hold a slot for its whole life.
	ok := context.Pool(runCtx).Default().Submit(
		runCtx,
		func() {
			defer c.release()
			// Runs before the release above, which wakes the Waits on this run. A shared read that started before the
			// run wrote its final state must not be joined by them, so they start a fresh one.
			defer e.waitReads.Forget(runCtx, plan.ID)

			next := e.states.Start
			if started != nil {
				next = e.states.Recovery
			}

			req := statemachine.Request[sm.Data]{
				Ctx: runCtx,
				Data: sm.Data{
					Plan:            plan,
					RecoveryStarted: started,
				},
				Next: next,
			}

			// NOTE: We are not handling the error here, as we are not returning it to the caller
			// and doesn't actually matter. All errors are encapsulated in the Plan's state.
			_, err := e.runner(plan.Name, req)
			if err != nil {
				context.Log(runCtx).Error("plan execution failed", "id", plan.ID, "error", err)
			}
			if started != nil {
				// A no-op if sm.Recovery already reported.
				msg := fmt.Errorf("run for plan(%s) ended before recovery reported", plan.ID)
				if err != nil {
					msg = fmt.Errorf("run for plan(%s) ended before recovery reported: %w", plan.ID, err)
				}
				started.Report(errors.E(runCtx, errors.CatInternal, errors.TypeBug, msg))
			}
		},
	)
	if !ok {
		// Submit only refuses work whose context has ended, and nothing cancels runCtx before the run starts, so this
		// is a bug. Release the claim rather than hold it forever for a run that never started.
		err := errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("worker pool refused a run for plan(%s)", plan.ID))
		c.release()
		if started != nil {
			started.Report(err)
		}
		return err
	}
	// The claim is now a run; callers waiting on it can stop waiting. This is bound to c, so if the run has already
	// ended and a later claim holds the id, that claim is untouched.
	c.launched(started)
	return nil
}

// Resume takes over a Plan that storage records as Running but that has no run in flight in this process, and
// continues it from where it left off, the same way startup recovery does. Wait and the retry of Plans that startup
// recovery could not start use it. It returns once the run is set up, or with
// the error that kept it from starting. If a run for id is already in flight here, or the Plan has finished, Resume
// does nothing. A Plan that has not started returns a permanent error; use Start.
//
// Like startup recovery, a Plan not updated within the WithMaxLastUpdate limit cannot be recovered: Resume marks it
// Failed with FRExceedRecovery instead of running it and returns nil, so a following Wait reports it finished.
//
// Like startup recovery, Resume assumes this process is the only one executing Plans from this storage.
func (e *Plans) Resume(ctx context.Context, id uuid.UUID) error {
	// Claim before reading, as Start does. A Resume that read Running and claimed afterwards could claim the ID after
	// another run finished the Plan and execute its stale Running copy again.
	runCtx, c, won, err := e.claimDecided(ctx, id)
	if err != nil || !won {
		return err
	}

	plan, err := e.readStored(ctx, id)
	if err != nil {
		c.release()
		return err
	}
	switch plan.State.Get().Status {
	case workflow.NotStarted:
		c.release()
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan(%s) is not started, use Start: %w", id, errors.ErrPermanent))
	case workflow.Running:
	default:
		c.release()
		return nil
	}

	if isAgedOut(ctx, plan, e.maxLastUpdate, e.now()) {
		defer c.release()
		// Runs before the release above, which wakes the Waits on this claim. As in launch, a shared read that started
		// before the Plan was written Failed must not be joined by them.
		defer e.waitReads.Forget(ctx, id)
		context.Log(ctx).Warn("coercion: plan is too old to resume, marking it failed", "id", id)
		if err := ageOut(ctx, e.store, plan); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("plan(%s) is too old to resume and could not be marked failed: %w", id, err))
		}
		return nil
	}

	started := sm.NewStarted()
	if err := e.launch(ctx, runCtx, c, plan, started); err != nil {
		return err
	}
	if err := started.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			// Only this caller stopped waiting. The run was launched and goes on setting up in the background.
			return errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("stopped waiting for plan(%s) to resume: %w", id, err))
		}
		// Reports from sm.Recovery and launch are already typed; E returns them unchanged.
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
	}
	return nil
}

func (e *Plans) now() time.Time {
	return time.Now().UTC()
}

// Wait waits for a Plan to finish execution and returns it as stored. Cancelling the Context stops waiting and returns
// a TypeTimeout error that wraps the Context's cause (so errors.Is(err, context.Canceled) holds). Once no run for the
// Plan is in flight in this process (at once, or when the run here ends), the outcome comes from storage, so a run
// that ended without recording a final state is not reported as finished. If storage records the Plan as Running, Wait
// takes it over with Resume and waits on that run; like startup recovery, this assumes this process is the only one
// executing Plans from this storage. Wait returns:
//   - the Plan and nil if the Plan has finished.
//   - a permanent error (errors.ErrPermanent) if the Plan does not exist or has not been started.
//   - the error from Resume if the Plan could not be resumed.
//   - a storage error if storage could not be read. It wraps errors.ErrPermanent if the store already ran out of
//     retries.
func (e *Plans) Wait(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	for {
		if waiter, ok := e.running.wait(id); ok {
			// waiter is only closed. A close wins over a done ctx, so a run that ended as ctx did is still reported.
			if _, r := chans.Get(ctx, waiter); !r.Closed() {
				return nil, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("stopped waiting for plan(%s): %w", id, context.Cause(ctx)))
			}
		}
		plan, err := e.storedOutcome(ctx, id)
		if err != nil {
			return nil, err
		}
		if plan.GetState().Status != workflow.Running {
			return plan, nil
		}
		// Storage records the Plan as Running, but no run for it is in flight here: a run ended without recording a
		// final state, or startup recovery did not see the Plan. Take it over, then wait on the run Resume started, or
		// read the outcome again if Resume found it finished or aged it out.
		if err := e.Resume(ctx, id); err != nil {
			return nil, err
		}
	}
}

// storedOutcome reads what storage says about a Plan that has no run in flight in this process. A Plan that has not
// been started is a permanent error. See Wait.
func (e *Plans) storedOutcome(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	plan, err := e.readShared(ctx, id)
	if err != nil {
		return nil, err
	}
	if plan.GetState().Status == workflow.NotStarted {
		return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, fmt.Errorf("plan(%s) is not started: %w", id, errors.ErrPermanent))
	}
	return plan, nil
}

// readShared reads a Plan from storage for Wait. Waits on one Plan at the same time share a single readStored, and each
// gets its own deep copy of the result, since callers may change the Plan they get and the store returns a fresh one
// from every Read. The shared read runs detached from any one caller's cancellation, so a caller that stops waiting
// does not fail the read for the rest; each caller stops waiting when its own Context ends, with the same TypeTimeout
// error Wait returns. The read is still bounded by the store's own retry limits.
func (e *Plans) readShared(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	readCtx := context.WithoutCancel(ctx)
	ch := e.waitReads.DoChan(ctx, id, func() (*workflow.Plan, error) { return e.readStored(readCtx, id) })
	res, r := chans.Get(ctx, ch)
	if !r.OK() {
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("stopped waiting for plan(%s): %w", id, context.Cause(ctx)))
	}
	if res.Err != nil {
		return nil, res.Err
	}
	return clone.Plan(ctx, res.Val, clone.WithKeepState(), clone.WithKeepSecrets()), nil
}

// readStored reads a Plan from storage for Start, Resume and Wait (through readShared). A missing Plan is a permanent
// not-found error, since retrying cannot make it appear; any other failure is a storage error, which is permanent only
// if the store already ran out of retries.
func (e *Plans) readStored(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	plan, err := e.store.Read(ctx, id)
	switch {
	case err != nil && errors.IsNotFound(err):
		return nil, errors.ErrNotFound(ctx, fmt.Errorf("plan(%s): %w: %w", id, err, errors.ErrPermanent))
	case err != nil:
		// E returns an error that is already typed unchanged.
		return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, err)
	case plan == nil:
		return nil, errors.ErrNotFound(ctx, fmt.Errorf("plan(%s): %w: %w", id, storage.ErrNotFound, errors.ErrPermanent))
	}
	return plan, nil
}

type ider interface {
	GetID() uuid.UUID
	SetID(uuid.UUID)
}

// validateStartState validates that the plan is in a valid state to be started.
func (p *Plans) validateStartState(plan *workflow.Plan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if p.maxSubmit == 0 {
		return fmt.Errorf("maxSubmit is zero")
	}
	if plan.SubmitTime.IsZero() {
		return fmt.Errorf("Plan.SubmitTime is zero")
	}

	if plan.SubmitTime.Add(p.maxSubmit).Before(time.Now()) {
		return fmt.Errorf("plan is stale, submit time is too old")
	}

	for item := range walk.Plan(plan) {
		for _, v := range p.validators.All() {
			if err := v(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Plans) validatePlan(i walk.Item) error {
	if i.Value.Type() != workflow.OTPlan {
		return nil
	}

	plan := i.Value.(*workflow.Plan)
	if plan.SubmitTime.IsZero() {
		return fmt.Errorf("Plan.SubmitTime is zero")
	}
	if plan.Reason != workflow.FRUnknown {
		return fmt.Errorf("Plan.Reason is not FRUnknown")
	}
	return nil
}

func (p *Plans) validateAction(i walk.Item) error {
	if i.Value.Type() != workflow.OTAction {
		return nil
	}

	action := i.Value.(*workflow.Action)

	if len(action.Attempts.Get()) != 0 {
		return fmt.Errorf("action(%s).Attempts was non-nil", action.Name)
	}

	plug := p.registry.Plugin(action.Plugin)
	if plug == nil {
		return fmt.Errorf("plugin(%s) not found", action.Plugin)
	}

	switch i.Chain[len(i.Chain)-1].Type() {
	case workflow.OTCheck:
		if !plug.IsCheck() {
			return fmt.Errorf("plugin(%s) is not a check plugin, but in a Checks object", action.Plugin)
		}
	}
	return nil
}

// validateID validates that the object has a non-nil ID.
func (e *Plans) validateID(i walk.Item) error {
	const v7 = uuid.Version(byte(7))

	if hasID, ok := i.Value.(ider); ok {
		if hasID.GetID() == uuid.Nil {
			return fmt.Errorf("Object(%T): ID is nil", i.Value)
		}
		if hasID.GetID().Version() != v7 {
			return fmt.Errorf("Object(%T): ID is not a V7 UUID", i.Value)
		}
		return nil
	}
	return fmt.Errorf("Object(%T): does not implement ider", i.Value)
}

// validateState validates that the object is in a valid state to be started.
func (e *Plans) validateState(i walk.Item) error {
	if get, ok := i.Value.(walk.Stateful); ok {
		state := get.GetState()
		if state.Status != workflow.NotStarted {
			return fmt.Errorf("internal status is not NotStarted")
		}
		if !state.Start.IsZero() {
			return fmt.Errorf("internal start is not zero")
		}
		if !state.End.IsZero() {
			return fmt.Errorf("internal end is not zero")
		}
		return nil
	}
	return fmt.Errorf("Object(%T): does not implement getStater", i.Value)
}
