// Package sm holds the states of our executor statemachine.
package sm

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/element-of-surprise/coercion/internal/execute/sm/actions"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"

	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/telemetry/log"
	"github.com/gostdlib/base/values/chans"
	"github.com/gostdlib/base/values/generics/result"
)

// block is a wrapper around a workflow.Block that contains additional information for the statemachine.
type block struct {
	block *workflow.Block

	contCancel      context.CancelFunc
	contCheckResult chan error
}

// Data represents the data that is passed between states.
type Data struct {
	// Plan is the workflow.Plan that is being executed.
	Plan *workflow.Plan

	// RecoveryStarted is reported once recovery has finished updating the data store with the fixed state of the
	// Plan and is off to execute the next state, or with the error that stopped it. nil when not recovering.
	RecoveryStarted *Started

	// recovered indicates whether we are recovering a Plan after a crash.
	recovered bool

	// blocks is a list of blocks that are being executed. These are removed as each block is completed.
	blocks []block
	// contCancel is the context.CancelFunc that will cancel the continuous check for the Plan.
	contCancel context.CancelFunc
	// contCheckResult is the channel that will receive the result of the continuous check for the Plan.
	contCheckResult chan error
	// stopHeartbeat stops the heartbeat startHeartbeat started and waits for it to exit. nil if none was started.
	stopHeartbeat func()
	// cut are the actions of the recovered block in blocks[0] that the crash cut mid-attempt after the Plan's ContChecks
	// failed. FinishCutBlock runs them again to completion.
	cut []cutAction

	err error
}

// contChecksPassing reports whether the continuous checks on the Plan or the current block have failed. If one has, it
// returns the type of the object whose checks failed (OTPlan or OTBlock) and the failure. The Plan's channel is polled
// first, then the block's, each on its own: a select over both picks at random among ready channels, and a closed
// channel (no ContChecks) or a waiting pass is always ready, so it would miss a waiting failure on the other.
func (d Data) contChecksPassing() (workflow.ObjectType, error) {
	if err := contFailure(d.contCheckResult); err != nil {
		return workflow.OTPlan, err
	}
	if len(d.blocks) == 0 {
		return workflow.OTUnknown, nil
	}
	if err := contFailure(d.blocks[0].contCheckResult); err != nil {
		return workflow.OTBlock, err
	}
	return workflow.OTUnknown, nil
}

// contFailure takes a waiting result off ch, if there is one, and returns it if it is a failure. A nil or closed ch,
// an empty ch and a waiting pass all return nil.
func contFailure(ch chan error) error {
	if ch == nil {
		return nil
	}
	if err, ok, _ := chans.TryGet(ch); ok && err != nil {
		return err
	}
	return nil
}

type nower func() time.Time

// actionRunner is a function that runs an action. We use this to fake out the action runner in tests.
type actionRunner func(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error

// checksRunner is a function that runs checks. We use this to fake out the check runner in tests.
type checksRunner func(ctx context.Context, checks *workflow.Checks) error

// actionsParallelRunner is a function that runs a list of actions in parallel. We use this to fake out the action runner in tests.
type actionsParallelRunner func(ctx context.Context, actions []*workflow.Action) error

// States is the statemachine that handles the execution of a Plan.
type States struct {
	store    storage.Vault
	registry *registry.Register

	actionsSM actions.Runner

	// nower is the function that returns the current time. This is set to time.Now by default.
	nower nower
	// maxLastUpdate is how long a Plan may go without an update before recovery treats it as abandoned. The heartbeat
	// writes often enough to stay inside it. Zero uses the heartbeat's defaults.
	maxLastUpdate time.Duration

	// testChecksRunner is the function that runs checks. If set, runChecksOnce calls this and returns.
	// We use this to fake out the check runner in tests.
	testChecksRunner checksRunner
	// testActionsParallelRunner is the function that runs a list of actions in parallel. If set, runParrallelActions calls this and returns.
	testActionsParallelRunner actionsParallelRunner
	// testActionRunner is the function that runs an action. If set, runAction calls this and returns.
	// We use this to fake out the action runner in tests.
	testActionRunner actionRunner
}

// New creates a new States statemachine. maxLastUpdate is how long a Plan may go without an update before recovery
// treats it as abandoned; the heartbeat writes often enough to stay inside it.
func New(ctx context.Context, store storage.Vault, registry *registry.Register, maxLastUpdate time.Duration) (*States, error) {
	if store == nil {
		return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, errors.New("store is required"))
	}
	s := &States{
		store:         store,
		registry:      registry,
		maxLastUpdate: maxLastUpdate,
	}
	return s, nil
}

// Start starts execution of the Plan. This is the first state of the statemachine for new Plans.
// If the Plan has been recovered after a crash, the statemachine starts with the Recover state.
func (s *States) Start(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan

	req.Ctx = context.SetPlanID(req.Ctx, req.Data.Plan.ID)

	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)

	startTime := s.now()
	state := plan.State.Get()
	state.Status = workflow.Running
	state.Start = startTime
	plan.State.Set(state)
	plan.RuntimeUpdate.Set(startTime)

	if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}

	req = s.startHeartbeat(req)

	req.Next = s.PlanBypassChecks
	return req
}

// startHeartbeat keeps the Plan's RuntimeUpdate fresh while it runs, so startup recovery does not age a live Plan out.
// Both a fresh start and a recovered run must call it. It returns req with Data.stopHeartbeat set; End calls that
// before it works out and writes the Plan's final state, because a heartbeat write landing then could store the final
// Plan before its objects, which recovery relies on never happening (see WriteObject).
func (s *States) startHeartbeat(req statemachine.Request[Data]) statemachine.Request[Data] {
	hbCtx, cancel := context.WithCancel(req.Ctx)
	done := result.New[struct{}]()

	// Copy req for the background goroutine to avoid a race with the caller's later req.Next assignment.
	reqCopy := req
	// The heartbeat lives as long as the Plan runs, so it goes on the default pool and never holds a slot in a
	// caller's Limited pool. It is a pool job rather than a context.Tasks Run because nothing about it should be
	// retried: its loop already survives a failed beat (runtimeUpdate only logs), it ends on purpose when stopped or
	// the Plan leaves Running, and stopHeartbeat must join it (done) before End writes the final state.
	ok := context.Pool(req.Ctx).Default().Submit(
		hbCtx,
		func() {
			defer done.Set(struct{}{}, nil)
			s.updateLastUpdate(hbCtx, reqCopy)
		},
	)
	if !ok {
		// Without a heartbeat, a restart can age this Plan out while it is still running.
		context.Log(req.Ctx).Error(fmt.Sprintf("plan(%s) heartbeat did not start: %v", req.Data.Plan.ID, context.Cause(hbCtx)))
		done.Set(struct{}{}, nil)
	}

	req.Data.stopHeartbeat = func() {
		cancel()
		// Wait on a Context that does not end, so a write the heartbeat has in flight finishes before End writes.
		_, _ = done.Wait(context.WithoutCancel(req.Ctx))
	}
	return req
}

// heartbeat returns how long a Plan may go without an update before the heartbeat writes one (write), and how often
// the heartbeat checks (tick), for a Plan that recovery treats as abandoned after maxLastUpdate. The defaults are 5
// minutes and 10 seconds. A shorter maxLastUpdate writes within a third of it, so a write that is slow or fails once
// still lands in time, and checks at least that often. Zero or less uses the defaults.
func heartbeat(maxLastUpdate time.Duration) (write, tick time.Duration) {
	write, tick = 5*time.Minute, 10*time.Second
	if maxLastUpdate <= 0 {
		return write, tick
	}
	write = min(write, maxLastUpdate/3)
	// A ticker's period must be positive.
	tick = max(min(tick, write), time.Millisecond)
	return write, tick
}

// updateLastUpdate periodically updates the last update time of the Plan while it is running.
func (s *States) updateLastUpdate(ctx context.Context, req statemachine.Request[Data]) {
	_, tick := heartbeat(s.maxLastUpdate)
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		// Stop on a tick that arrives after ctx is done (ResultOKCanceled) too, so no update starts once stopped.
		if _, r := chans.Get(ctx, t.C); r != chans.ResultOK {
			return
		}
		if req.Data.Plan.State.Get().Status != workflow.Running {
			return
		}
		s.runtimeUpdate(req.Ctx, req.Data.Plan)
	}
}

// runtimeUpdate updates the RuntimeUpdate time of the Plan if more time than heartbeat allows has passed since the
// last update.
func (s *States) runtimeUpdate(ctx context.Context, plan *workflow.Plan) {
	write, _ := heartbeat(s.maxLastUpdate)
	if s.now().Sub(walk.LastUpdate(ctx, plan)) > write {
		now := s.now()
		plan.RuntimeUpdate.Set(now)
		if err := s.store.UpdatePlan(ctx, plan); err != nil {
			context.Log(ctx).Error(fmt.Sprintf("failed to write Plan last update time: %v", err))
		}
	}
}

// PlanBypassChecks runs all the gates on the Plan. If any of the gates fail,
// or no gates are present, the Plan is executed.
func (s *States) PlanBypassChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	defer func() {
		if err := s.store.UpdatePlan(req.Ctx, req.Data.Plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	if skipRecoveredChecks(req.Data.Plan.BypassChecks) {
		req.Next = s.PlanPreChecks
		return req
	}

	skip := s.runBypasses(req.Ctx, req.Data.Plan.BypassChecks)
	if skip {
		if req.Data.contCheckResult != nil {
			close(req.Data.contCheckResult)
		}
		req.Next = s.End
		return req
	}
	req.Next = s.PlanPreChecks
	return req
}

// PlanPreChecks runs all PreChecks and ContChecks on the Plan before proceeding.
func (s *States) PlanPreChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	defer func() {
		if err := s.store.UpdatePlan(req.Ctx, req.Data.Plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	if skipRecoveredChecks(req.Data.Plan.PreChecks) {
		req.Next = s.PlanStartContChecks
		return req
	}

	err := s.runPreChecks(req.Ctx, req.Data.Plan.PreChecks, req.Data.Plan.ContChecks)
	if err != nil {
		req.Data.err = err
		req.Next = s.PlanDeferredActions
		return req
	}

	req.Next = s.PlanStartContChecks

	return req
}

// PlanStartContChecks starts the ContChecks of the Plan.
func (s *States) PlanStartContChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	req.Next = s.ExecuteBlock

	if req.Data.Plan.ContChecks == nil {
		close(req.Data.contCheckResult)
		return req
	}

	var ctx context.Context
	ctx, req.Data.contCancel = context.WithCancel(req.Ctx)
	s.startContChecks(ctx, req.Data.Plan.ContChecks, req.Data.contCheckResult)
	return req
}

// startContChecks runs checks in a loop until ctx is canceled, sending results to results. The loop lives as long as
// the Plan or Block it guards, so it goes on the default pool and never holds a slot in a caller's Limited pool. If
// the pool refuses it, results gets an error and is closed, so nothing draining results waits forever.
func (s *States) startContChecks(ctx context.Context, checks *workflow.Checks, results chan error) {
	ok := context.Pool(ctx).Default().Submit(
		ctx,
		func() {
			s.runContChecks(ctx, checks, results)
		},
	)
	if ok {
		return
	}

	var err error
	switch cause := context.Cause(ctx); cause {
	case nil:
		err = errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("worker pool refused the continuous checks with a live Context"))
	default:
		err = errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("continuous checks did not start: %w", cause))
	}
	chans.TryPut(results, err)
	close(results)
}

// ExecuteBlock executes the current block.
func (s *States) ExecuteBlock(req statemachine.Request[Data]) statemachine.Request[Data] {
	// No more blocks, the Plan is done.
	if len(req.Data.blocks) == 0 {
		req.Next = s.PlanPostChecks
		return req
	}

	// ExecuteSequences polls the Plan's ContChecks only while a block's sequences run. A failure that arrived after
	// them, during the previous block's PostChecks, DeferredChecks or ExitDelay, is read here, so it fails the Plan
	// before this block runs its EntranceDelay, BypassChecks and PreChecks. The block is left untouched. Only a result
	// that has already arrived is read: no ContChecks pass is started here.
	if err := contFailure(req.Data.contCheckResult); err != nil {
		req.Data.err = err
		req.Next = s.PlanDeferredActions
		return req
	}

	h := req.Data.blocks[0]

	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	if skipBlock(h) {
		if len(req.Data.blocks) == 1 {
			req.Data.blocks = nil
		} else {
			req.Data.blocks = req.Data.blocks[1:]
		}
		req.Next = s.ExecuteBlock
		return req
	}

	// A recovered block that was Running keeps its start and has had its entrance delay. It goes through its bypass,
	// pre and continuous checks again like a new block (checks that completed are skipped), so its recovered Running
	// sequences resume in ExecuteSequences guarded by the continuous checks rather than running unguarded first.
	if !req.Data.recovered || h.block.GetState().Status != workflow.Running {
		if err := after(req.Ctx, h.block.EntranceDelay); err != nil {
			state := h.block.State.Get()
			state.Status = workflow.Stopped
			h.block.State.Set(state)
			req.Data.err = err
			req.Next = s.PlanDeferredActions
			return req
		}
		state := h.block.State.Get()
		state.Status = workflow.Running
		state.Start = s.now()
		h.block.State.Set(state)
	}

	req.Next = s.BlockBypassChecks
	return req
}

// BlockBypassChecks runs all the gates on the Block. If any of the gates fail,
// or no gates are present, the Block is executed.
func (s *States) BlockBypassChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	if h.block.BypassChecks == nil || h.block.BypassChecks.State.Get().Status == workflow.Failed {
		req.Next = s.BlockPreChecks
		return req
	}
	skip := s.runBypasses(req.Ctx, h.block.BypassChecks)
	if skip {
		if h.contCheckResult != nil {
			close(h.contCheckResult)
		}
		req.Next = s.BlockEnd
		return req
	}
	req.Next = s.BlockPreChecks
	return req
}

// BlockPreChecks runs all PreChecks and ContChecks on the current block before proceeding.
func (s *States) BlockPreChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	if h.block.PreChecks == nil || h.block.PreChecks.State.Get().Status == workflow.Completed {
		req.Next = s.BlockStartContChecks
		return req
	}

	err := s.runPreChecks(req.Ctx, h.block.PreChecks, h.block.ContChecks)
	if err != nil {
		if err := s.failSequences(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to settle Block sequences: %v", err)
		}
		s.failBlock(h.block)
		req.Data.err = err
		req.Next = s.BlockDeferredChecks
		return req
	}

	req.Next = s.BlockStartContChecks

	return req
}

// BlockStartContChecks starts the ContChecks of the current block.
func (s *States) BlockStartContChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	if h.block.ContChecks == nil {
		close(h.contCheckResult)
		req.Next = s.ExecuteSequences
		return req
	}

	var ctx context.Context
	ctx, h.contCancel = context.WithCancel(context.WithoutCancel(req.Ctx))
	// This re-assignment happens only here because a block is a stack object
	// and the other fields are all pointers that are assigned at the beginning of the block.
	// But contextCanel is not, so it needs to be re-assigned here.
	req.Data.blocks[0] = h

	s.startContChecks(ctx, h.block.ContChecks, h.contCheckResult)

	req.Next = s.ExecuteSequences
	return req
}

// ExecuteSequences executes the sequences of the current block.
func (s *States) ExecuteSequences(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	// data is the copy of req.Data the launched sequences poll the continuous checks with.
	data := req.Data

	failures := atomic.Int64{}

	for _, seq := range h.block.Sequences {
		if seq.State.Get().Status == workflow.Failed {
			failures.Add(1)
		}
	}

	pool := context.Pool(req.Ctx).Limited(req.Ctx, "ExecuteSequences", h.block.Concurrency)
	g := pool.Group()

	// contErr is the first continuous checks failure seen. Polling takes a failure off its result channel, so it is
	// kept here for every later launch, and the poll after they finish, to see.
	var contErr sync.MutexValue[error]
	contFailed := func() error {
		var err error
		contErr.WithLock(func(p *error) {
			if *p == nil {
				_, *p = data.contChecksPassing()
			}
			err = *p
		})
		return err
	}

	// stopErr is why no more sequences are launched. The sequences already launched still run to completion before
	// this state returns: the states after it write the block and Plan as final, and a sequence still running would
	// write after them.
	var stopErr error
	for i := 0; i < len(h.block.Sequences); i++ {
		seq := h.block.Sequences[i]
		seqStatus := seq.State.Get().Status
		if seqStatus == workflow.Completed || seqStatus == workflow.Failed {
			continue
		}

		if err := contFailed(); err != nil {
			stopErr = err
			break
		}

		if s.exceededFailures(h.block, failures.Load()) {
			stopErr = errors.ErrPlugin(req.Ctx, fmt.Errorf("block(%s) has exceeded the tolerated failures", h.block.Name))
			break
		}

		// g.Go blocks until the Limited pool has a free slot, and the continuous checks can fail or a running sequence
		// can fail while it waits. A sequence counts its failure before it gives its slot back, so checking both again
		// here, once this sequence holds a slot, keeps any sequence from starting after the continuous checks failed or
		// the tolerated failures are exceeded.
		g.Go(
			context.WithoutCancel(req.Ctx),
			func(ctx context.Context) error {
				if err := contFailed(); err != nil {
					return errors.ErrPlugin(ctx, fmt.Errorf("block(%s) continuous checks failed, sequence(%s) not started: %w", h.block.Name, seq.Name, err))
				}
				if s.exceededFailures(h.block, failures.Load()) {
					return errors.ErrPlugin(ctx, fmt.Errorf("block(%s) has exceeded the tolerated failures, sequence(%s) not started", h.block.Name, seq.Name))
				}

				err := s.execSeq(ctx, seq)
				if err != nil {
					failures.Add(1)
				}
				return err
			},
		)
	}

	// We don't care about the error here, we just want to wait for all sequences to finish.
	_ = g.Wait(context.WithoutCancel(req.Ctx))

	// A launched sequence may have found the continuous checks failed and taken that failure off its channel, and a
	// failure may have arrived after every launch polled, while the last sequences ran. Poll once more so that failure
	// fails this block rather than leaking into the next one, which would otherwise run its BypassChecks and PreChecks
	// first. This only reads a result that has already arrived: no ContChecks pass is started here. It goes through
	// contFailed so a failure a launch already took is not lost. This agrees with BlockEnd's drain of the block's
	// ContChecks: a block failure read here fails the block now, and BlockEnd still drains whatever is left on the
	// block's channel, so either way a block ContChecks failure fails this block and never reaches the next one.
	if stopErr == nil {
		stopErr = contFailed()
	}
	// Need to recheck in case the last sequence failed and sent us over the edge.
	if stopErr == nil && s.exceededFailures(h.block, failures.Load()) {
		stopErr = errors.ErrPlugin(req.Ctx, fmt.Errorf("block(%s) has exceeded the tolerated failures", h.block.Name))
	}
	if stopErr != nil {
		if err := s.failSequences(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to settle Block sequences: %v", err)
		}
		s.failBlock(h.block)
		req.Data.err = stopErr
		req.Next = s.BlockDeferredChecks
		return req
	}

	req.Next = s.BlockPostChecks
	return req
}

// FinishCutBlock finishes the recovered block a crash cut short after the Plan's ContChecks failed. Each action the crash
// cut mid-attempt runs again to completion, and then nothing new starts, as ExecuteSequences starts nothing once the
// ContChecks fail: a sequence with actions left is settled Failed and one not started stays NotStarted. The block's
// PostChecks always run to completion once its sequences are done, and its ContChecks are not run again. The block then
// goes on through BlockDeferredChecks and BlockEnd as in a live run, and no later block starts.
func (s *States) FinishCutBlock(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()
	req.Next = s.BlockDeferredChecks

	// As in ExecuteSequences, the work runs to completion whatever happens to req.Ctx: the states after this one write
	// the block as final, and work still running would write after them.
	ctx := context.WithoutCancel(req.Ctx)
	g := context.Pool(ctx).Group()
	for _, c := range req.Data.cut {
		g.Go(ctx, func(ctx context.Context) error {
			err := s.runAction(ctx, c.action, s.store)
			s.endCutSeq(ctx, c.seq)
			return err
		})
	}
	// Each action's state is the source of truth, so the error is not needed.
	_ = g.Wait(ctx)

	if err := s.failSequences(ctx, h.block); err != nil {
		log.Fatalf("failed to settle Block sequences: %v", err)
	}

	if !s.seqsDone(h.block) {
		s.failBlock(h.block)
		return req
	}
	if h.block.PostChecks != nil && !isCompleted(h.block.PostChecks) {
		if err := s.runChecksOnce(ctx, h.block.PostChecks); err != nil {
			s.failBlock(h.block)
			req.Data.err = err
			return req
		}
	}
	if checksFailed(h.block.PreChecks) || checksFailed(h.block.ContChecks) || checksFailed(h.block.PostChecks) {
		s.failBlock(h.block)
	}
	// Otherwise the block stays Running for BlockEnd to complete.
	return req
}

// endCutSeq ends seq once its action that the crash cut mid-attempt has run again: Completed if every action in it
// completed, otherwise Failed, as a sequence whose action failed or that the ContChecks failure stopped with actions
// left.
func (s *States) endCutSeq(ctx context.Context, seq *workflow.Sequence) {
	status := workflow.Completed
	for _, a := range seq.Actions {
		if a.State.Get().Status != workflow.Completed {
			status = workflow.Failed
			break
		}
	}
	state := seq.State.Get()
	state.Status = status
	state.End = s.now()
	seq.State.Set(state)
	if err := s.store.UpdateSequence(ctx, seq); err != nil {
		log.Fatalf("failed to write Sequence: %v", err)
	}
}

// seqsDone reports whether every sequence of b has finished with no more failures than b tolerates, which is when a
// live run goes on to the block's PostChecks.
func (s *States) seqsDone(b *workflow.Block) bool {
	var failures int64
	for _, seq := range b.Sequences {
		switch seq.State.Get().Status {
		case workflow.Completed:
		case workflow.Failed:
			failures++
		default:
			return false
		}
	}
	return !s.exceededFailures(b, failures)
}

// failBlock sets b to Failed.
func (s *States) failBlock(b *workflow.Block) {
	state := b.State.Get()
	state.Status = workflow.Failed
	b.State.Set(state)
}

// exceededFailures reports whether failures is more than block tolerates.
func (s *States) exceededFailures(block *workflow.Block, failures int64) bool {
	return block.ToleratedFailures >= 0 && failures > int64(block.ToleratedFailures)
}

// BlockPostChecks runs all PostChecks on the current block.
func (s *States) BlockPostChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()
	req.Next = s.BlockDeferredChecks
	if checksCompleted(h.block.PostChecks) {
		return req
	}

	err := s.runChecksOnce(req.Ctx, h.block.PostChecks)
	if err != nil {
		s.failBlock(h.block)
		req.Data.err = err
		return req
	}
	return req
}

// BlockDeferredChecks runs all DeferredChecks on the current block before proceeding.
func (s *States) BlockDeferredChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]
	defer func() {
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()
	req.Next = s.BlockEnd

	if checksCompleted(h.block.DeferredChecks) {
		return req
	}

	err := s.runChecksOnce(req.Ctx, h.block.DeferredChecks)
	if err != nil {
		s.failBlock(h.block)
		req.Data.err = err
		return req
	}

	return req
}

// BlockEnd ends the current block and moves to the next block.
func (s *States) BlockEnd(req statemachine.Request[Data]) statemachine.Request[Data] {
	h := req.Data.blocks[0]

	defer func() {
		state := h.block.State.Get()
		state.End = s.now()
		h.block.State.Set(state)
		if err := s.store.UpdateBlock(req.Ctx, h.block); err != nil {
			log.Fatalf("failed to write Block: %v", err)
		}
	}()

	// Don't use checksCompleted() here, we want to run the block if it is not completed.
	if h.block.BypassChecks != nil && h.block.BypassChecks.State.Get().Status == workflow.Completed {
		state := h.block.State.Get()
		state.Status = workflow.Completed
		h.block.State.Set(state)
	} else {
		// For safety reasons, we always check this so we don't get goroutine leaks.
		if h.contCancel != nil {
			h.contCancel()
		}

		// Stop our cont checks if they are still running, get the final result. contCancel is set only when
		// BlockStartContChecks started them; a block whose PreChecks failed never did, and nothing would ever send on
		// or close their result channel.
		if h.block.ContChecks != nil && h.contCancel != nil {
			var err error
			for err = range h.contCheckResult {
				if err != nil {
					break
				}
			}
			if err != nil {
				s.failBlock(h.block)
				req.Data.err = err
				req.Next = s.PlanDeferredActions
				return req
			}
		}

		if h.block.State.Get().Status == workflow.Running {
			state := h.block.State.Get()
			state.Status = workflow.Completed
			h.block.State.Set(state)
		} else {
			s.failBlock(h.block)
			req.Next = s.PlanDeferredActions
			return req
		}

		if err := after(req.Ctx, h.block.ExitDelay); err != nil {
			state := h.block.State.Get()
			state.Status = workflow.Stopped
			h.block.State.Set(state)
			req.Data.err = err
			req.Next = s.PlanDeferredActions
			return req
		}
	}

	if len(req.Data.blocks) == 1 {
		req.Data.blocks = nil
	} else {
		req.Data.blocks = req.Data.blocks[1:]
	}
	req.Next = s.ExecuteBlock
	return req
}

// PlanPostChecks stops the ContChecks and runs the PostChecks of the current plan.
func (s *States) PlanPostChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	// No matter what the outcome here is, we go to the end state.
	req.Next = s.PlanDeferredActions
	defer func() {
		if err := s.store.UpdatePlan(req.Ctx, req.Data.Plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	// We always checks this to avoid programmer mistakes that lead to a goroutine leak.
	if req.Data.contCancel != nil {
		req.Data.contCancel()
	}

	// contCancel is set only when PlanStartContChecks started the ContChecks; otherwise nothing would ever send on or
	// close their result channel.
	if req.Data.Plan.ContChecks != nil && req.Data.contCancel != nil {
		for err := range req.Data.contCheckResult {
			if err != nil {
				req.Data.err = err
				return req
			}
		}
	}
	// A recovered Plan whose ContChecks had failed goes on to its deferred work once FinishCutBlock and BlockEnd finish
	// the block they cut short. Its PostChecks do not run, as the drain above returns on that failure in a live run.
	if checksFailed(req.Data.Plan.ContChecks) {
		return req
	}
	// A recovered Plan whose blocks were all done has no ContChecks loop to drain, but a pass the crash cut short was
	// reset to NotStarted. Run it once, to completion, here where a live run drains it: its result counts as the drained
	// pass's would. No loop is started, so no new pass follows it.
	if req.Data.recovered && req.Data.contCancel == nil && contPassCut(req.Data.Plan.ContChecks) {
		if err := s.runChecksOnce(context.WithoutCancel(req.Ctx), req.Data.Plan.ContChecks); err != nil {
			req.Data.err = err
			return req
		}
	}

	if req.Data.Plan.PostChecks != nil && !isCompleted(req.Data.Plan.PostChecks) {
		if err := s.runChecksOnce(req.Ctx, req.Data.Plan.PostChecks); err != nil {
			req.Data.err = err
			return req
		}
	}
	return req
}

// PlanDeferredChecks runs the DeferredChecks.
func (s *States) PlanDeferredChecks(req statemachine.Request[Data]) statemachine.Request[Data] {
	// No matter what the outcome here is, we go to End.
	req.Next = s.End
	defer func() {
		if err := s.store.UpdatePlan(req.Ctx, req.Data.Plan); err != nil {
			log.Fatalf("failed to write Plan: %v", err)
		}
	}()

	if req.Data.Plan.DeferredChecks == nil || isCompleted(req.Data.Plan.DeferredChecks) {
		return req
	}
	if err := s.runChecksOnce(req.Ctx, req.Data.Plan.DeferredChecks); err != nil {
		req.Data.err = err
		return req
	}
	return req
}

// PlanDeferredActions runs the DeferredActions batches chosen by OnFailure or OnSuccess
// based on the Plan's accumulated state prior to DeferredChecks (which run after this
// state). It always transitions to PlanDeferredChecks. If any batch with FailElement=true
// fails, the DeferredActions container is marked Failed and finalStates will fail the Plan
// with FRDeferredAction. Idempotent across recovery: if DeferredActions is already
// in a terminal state, this is a no-op.
func (s *States) PlanDeferredActions(req statemachine.Request[Data]) statemachine.Request[Data] {
	req.Next = s.PlanDeferredChecks

	da := req.Data.Plan.DeferredActions
	if da == nil {
		return req
	}

	defer func() {
		if err := s.store.UpdateDeferredActions(req.Ctx, da); err != nil {
			log.Fatalf("failed to write DeferredActions: %v", err)
		}
	}()

	if isCompleted(da) {
		return req
	}

	state := da.State.Get()
	state.Status = workflow.Running
	state.Start = s.now()
	da.State.Set(state)
	if err := s.store.UpdateDeferredActions(req.Ctx, da); err != nil {
		log.Fatalf("failed to write DeferredActions: %v", err)
	}

	failed := planHasFailed(req.Data.Plan, req.Data.err)
	batches := selectDeferredBatches(da.DeferredBatches, failed)

	failElementTripped := s.runDeferredActions(req.Ctx, batches)

	state = da.State.Get()
	state.End = s.now()
	if failElementTripped {
		state.Status = workflow.Failed
	} else {
		state.Status = workflow.Completed
	}
	da.State.Set(state)
	return req
}

// selectDeferredBatches filters batches whose When matches the plan outcome.
// Always batches always run; OnSuccess runs only if !failed; OnFailure only if failed.
// Order is preserved.
func selectDeferredBatches(batches []*workflow.DeferBatch, failed bool) []*workflow.DeferBatch {
	if len(batches) == 0 {
		return nil
	}
	out := make([]*workflow.DeferBatch, 0, len(batches))
	for _, b := range batches {
		switch b.When {
		case workflow.Always:
			out = append(out, b)
		case workflow.OnSuccess:
			if !failed {
				out = append(out, b)
			}
		case workflow.OnFailure:
			if failed {
				out = append(out, b)
			}
		}
	}
	return out
}

// planHasFailed reports whether any phase that ran before DeferredActions
// failed. Used by PlanDeferredActions to filter DeferredBatches by When.
// DeferredChecks are not consulted here because they run after DeferredActions.
func planHasFailed(p *workflow.Plan, reqErr error) bool {
	if reqErr != nil {
		return true
	}
	if checksFailed(p.PreChecks) || checksFailed(p.ContChecks) || checksFailed(p.PostChecks) {
		return true
	}
	for _, b := range p.Blocks {
		if b.State.Get().Status == workflow.Failed {
			return true
		}
	}
	return false
}

// End is the final state of the state machine. This is always the last state, regardless of errors.
// This will do the calculations of the final state of the Plan.
func (s *States) End(req statemachine.Request[Data]) statemachine.Request[Data] {
	plan := req.Data.Plan
	defer func() {
		state := plan.State.Get()
		state.End = s.now()
		plan.State.Set(state)
		s.writeEverything(req.Ctx, plan)
	}()

	// PlanPostChecks stops and drains the Plan's ContChecks, but every failure path skips it. Stop them here too and
	// wait for them: a pass runs detached from cancellation, so one in flight would still be setting the ContChecks'
	// state while the final state is worked out and would write after writeEverything. contCancel is set only when
	// PlanStartContChecks started them; otherwise nothing would ever send on or close the result channel. Draining a
	// channel PlanPostChecks already drained ends at once.
	if req.Data.contCancel != nil {
		req.Data.contCancel()
		if req.Data.Plan.ContChecks != nil {
			for err := range req.Data.contCheckResult {
				if err != nil && req.Data.err == nil {
					req.Data.err = err
				}
			}
		}
	}

	// Stop the heartbeat only now: the Plan is still Running while an in-flight ContChecks pass is drained above, which
	// may take a while, and a heartbeat that stopped first would let a restart age the Plan out as abandoned. It must
	// stop before the final state is worked out below and written by the deferred writeEverything, so no heartbeat
	// write lands after them. stopHeartbeat is nil when Recovery sent the Plan straight here because it was already
	// finished or had nothing left to run.
	if req.Data.stopHeartbeat != nil {
		req.Data.stopHeartbeat()
	}

	// Runs a new statemachine to calculate the final state of the Plan.
	f := finalStates{}
	req.Next = f.start

	var err error
	req, err = statemachine.Run("finalStates", req)
	if errors.IsBug(err) {
		context.Log(req.Ctx).Error(fmt.Sprintf("failed to calculate final state of Plan: %s", err))
	}
	req.Next = nil

	// Promote Data.err to the request if it is not nil.
	if req.Data.err != nil {
		req.Err = req.Data.err
	}

	return req
}

func (s *States) writeEverything(ctx context.Context, plan *workflow.Plan) {
	ctx = context.WithoutCancel(ctx)
	// With no snapshot, every object counts as changed and is written.
	if err := s.store.UpdateChanges(ctx, plan, nil); err != nil {
		log.Fatalf("failed to write Plan objects: %v", err)
	}

	if err := s.store.UpdatePlan(ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}
}

// runBypasses runs all gates in the Plan. If any gate fails, the Plan proceeds. If there
// are no gates, this function returns false as the plan should proceed.
func (s *States) runBypasses(ctx context.Context, bypasses *workflow.Checks) (skip bool) {
	if bypasses == nil {
		return false
	}

	if err := s.runChecksOnce(ctx, bypasses); err != nil {
		return false
	}
	return true
}

// runPreChecks runs all PreChecks and ContChecks. This is a helper function for PlanPreChecks and BlockPreChecks.
func (s *States) runPreChecks(ctx context.Context, preChecks *workflow.Checks, contChecks *workflow.Checks) error {
	if preChecks == nil && contChecks == nil {
		return nil
	}

	g := context.Pool(ctx).Group()

	if preChecks != nil {
		g.Go(ctx, func(ctx context.Context) error {
			return s.runChecksOnce(ctx, preChecks)
		})
	}

	if contChecks != nil {
		g.Go(ctx, func(ctx context.Context) error {
			return s.runChecksOnce(ctx, contChecks)
		})
	}

	return g.Wait(ctx)
}

// runContChecks runs the ContChecks in a loop with a delay between each run until the Context is cancelled.
// Each result goes to resultCh through sendContResult. If a check fails before the Context is cancelled, the
// error is sent and the function returns. resultCh is closed on return.
func (s *States) runContChecks(ctx context.Context, checks *workflow.Checks, resultCh chan error) {
	defer close(resultCh)

	// If the delay is less than or equal to 0, we set it to 1ns to avoid a panic,
	// since time.NewTicker panics if the duration is less than or equal to 0.
	delay := checks.Delay
	if delay <= 0 {
		delay = time.Nanosecond
	}

	// Run checks immediately on start to ensure they transition to Running state,
	// even if the plan completes before the first ticker fires.
	err := s.runChecksOnce(context.WithoutCancel(ctx), checks)
	sendContResult(resultCh, err)
	if err != nil {
		return
	}

	t := time.NewTicker(delay)
	defer t.Stop()

	for {
		t.Reset(delay)
		// Stop on a tick that arrives after ctx is done (ResultOKCanceled) too, so no pass starts once stopped.
		if _, r := chans.Get(ctx, t.C); r != chans.ResultOK {
			return
		}
		err := s.runChecksOnce(context.WithoutCancel(ctx), checks)
		sendContResult(resultCh, err)
		if err != nil {
			return
		}
	}
}

// sendContResult sends a ContChecks result on ch, which must have a buffer of one and runContChecks as its only
// sender. While sequences run, the only reader polls ch once per sequence launch and once after they all finish, so a
// pass is dropped when one is already waiting rather than parking the checks. A failure is the last result sent and
// must reach those polls or the drains in BlockEnd and PlanPostChecks, so it replaces a waiting pass.
func sendContResult(ch chan error, err error) {
	if chans.TryPut(ch, err) || err == nil {
		return
	}
	// The buffer holds a pass. Drop it unless a reader just took it; either way the buffer is now empty and nothing
	// else sends, so this send does not block.
	chans.TryGet(ch)
	ch <- err
}

// runChecksOnce runs Checks once and writes the result to the store.
func (s *States) runChecksOnce(ctx context.Context, checks *workflow.Checks) error {
	if s.testChecksRunner != nil {
		return s.testChecksRunner(ctx, checks)
	}

	resetActions(checks.Actions)

	state := checks.State.Get()
	state.Status = workflow.Running
	state.Start = s.now()
	checks.State.Set(state)

	if err := s.store.UpdateChecks(ctx, checks); err != nil {
		log.Fatalf("failed to write Checks: %v", err)
	}
	defer func() {
		if err := s.store.UpdateChecks(ctx, checks); err != nil {
			log.Fatalf("failed to write Checks: %v", err)
		}
	}()
	defer func() {
		state := checks.State.Get()
		state.End = s.now()
		checks.State.Set(state)
	}()

	if err := s.runActionsParallel(ctx, checks.Actions); err != nil {
		state := checks.State.Get()
		state.Status = workflow.Failed
		checks.State.Set(state)
		return err
	}
	state = checks.State.Get()
	state.Status = workflow.Completed
	checks.State.Set(state)
	return nil
}

// runActionsParallel runs a list of actions in parallel.
func (s *States) runActionsParallel(ctx context.Context, actions []*workflow.Action) error {
	if s.testActionsParallelRunner != nil {
		return s.testActionsParallelRunner(ctx, actions)
	}
	// Yes, we loop twice, but actions is small and we only want to write to the store once.
	for _, action := range actions {
		state := action.State.Get()
		state.Status = workflow.Running
		state.Start = s.now()
		action.State.Set(state)
		if err := s.store.UpdateAction(ctx, action); err != nil {
			log.Fatalf("failed to write Action: %v", err)
		}
	}

	g := context.Pool(ctx).Group()

	// Run the actions in parallel.
	for _, action := range actions {
		action := action

		g.Go(ctx, func(ctx context.Context) (err error) {
			return s.runAction(ctx, action, s.store)
		})
	}
	return g.Wait(ctx)
}

// execSeq executes a sequence of actions. Any Job failures fail the Sequnence. The Job may retry
// based on the retry policy.
func (s *States) execSeq(ctx context.Context, seq *workflow.Sequence) error {
	defer func() {
		if err := s.store.UpdateSequence(ctx, seq); err != nil {
			log.Fatalf("failed to write Sequence: %v", err)
		}
	}()

	switch seq.State.Get().Status {
	case workflow.Completed:
		return nil
	case workflow.Failed:
		for _, action := range seq.Actions {
			if action.State.Get().Status == workflow.Failed {
				attempts := action.Attempts.Get()
				return attempts[len(attempts)-1].Err
			}
		}
		// Well, this shouldn't happen, but we have a failed sequence with no failed actions.
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("sequence %s is already failed, but has no failed action", seq.Name))
	}

	state := seq.State.Get()
	state.Status = workflow.Running
	state.Start = s.now()
	seq.State.Set(state)
	if err := s.store.UpdateSequence(ctx, seq); err != nil {
		log.Fatalf("failed to write Sequence: %v", err)
	}
	defer func() {
		state := seq.State.Get()
		state.End = s.now()
		seq.State.Set(state)
	}()

	for _, action := range seq.Actions {
		if err := s.runAction(ctx, action, s.store); err != nil {
			state := seq.State.Get()
			state.Status = workflow.Failed
			seq.State.Set(state)
			return err
		}
	}

	state = seq.State.Get()
	state.Status = workflow.Completed
	seq.State.Set(state)
	return nil
}

// runDeferredActions runs the selected DeferBatch list. Batches execute in
// parallel; all batches are attempted regardless of individual failures.
// Returns true if any batch with FailElement=true ended in Failed state.
func (s *States) runDeferredActions(ctx context.Context, batches []*workflow.DeferBatch) bool {
	if len(batches) == 0 {
		return false
	}

	var tripped atomic.Bool
	g := context.Pool(ctx).Group()
	for _, batch := range batches {
		b := batch
		g.Go(ctx, func(ctx context.Context) error {
			s.runDeferBatch(ctx, b)
			if b.State.Get().Status == workflow.Failed && b.FailElement {
				tripped.Store(true)
			}
			return nil
		})
	}
	_ = g.Wait(ctx) // per-batch state is source of truth
	return tripped.Load()
}

// runDeferBatch runs a single DeferBatch: marks it Running, runs its Actions
// sequentially, stops at the first terminal (non-Completed) Action, and marks
// the batch Completed, Failed, or Stopped accordingly. Entry short-circuits on
// recovered terminal batch state, mirroring execSeq.
func (s *States) runDeferBatch(ctx context.Context, batch *workflow.DeferBatch) {
	defer func() {
		if err := s.store.UpdateDeferBatch(ctx, batch); err != nil {
			log.Fatalf("failed to write DeferBatch: %v", err)
		}
	}()

	switch batch.State.Get().Status {
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		return
	}

	state := batch.State.Get()
	state.Status = workflow.Running
	state.Start = s.now()
	batch.State.Set(state)
	if err := s.store.UpdateDeferBatch(ctx, batch); err != nil {
		log.Fatalf("failed to write DeferBatch: %v", err)
	}

	terminal := workflow.Completed
	for _, action := range batch.Actions {
		if isCompleted(action) {
			// Recovered terminal state: skip Completed; propagate Failed/Stopped
			// into the batch's terminal status to match fixDeferBatch semantics.
			switch action.State.Get().Status {
			case workflow.Failed:
				terminal = workflow.Failed
			case workflow.Stopped:
				terminal = workflow.Stopped
			default:
				continue
			}
			break
		}
		if err := s.runAction(ctx, action, s.store); err != nil {
			terminal = workflow.Failed
			break
		}
	}

	state = batch.State.Get()
	state.End = s.now()
	state.Status = terminal
	batch.State.Set(state)
}

// runAction runs an action and returns the response or an error. If the response is not the expected
// type, it returns a permanent error that prevents retries.
func (s *States) runAction(ctx context.Context, action *workflow.Action, updater storage.ActionUpdater) error {
	if s.testActionRunner != nil {
		return s.testActionRunner(ctx, action, updater)
	}
	defer func() {
		if err := s.store.UpdateAction(ctx, action); err != nil {
			log.Fatalf("failed to write Action: %v", err)
		}
	}()
	switch action.State.Get().Status {
	case workflow.Completed:
		return nil
	case workflow.Failed:
		attempts := action.Attempts.Get()
		return attempts[len(attempts)-1].Err
	}

	ctx = context.SetActionID(ctx, action.ID)

	req := statemachine.Request[actions.Data]{
		Ctx: ctx,
		Data: actions.Data{
			Action:   action,
			Updater:  updater,
			Registry: s.registry,
		},
		Next: s.actionsSM.Start,
	}
	_, err := statemachine.Run("run action statemachine", req)
	if err != nil {
		return err
	}
	return nil
}

// now returns the current time in UTC. If a nower is set, it uses that to get the time.
func (s *States) now() time.Time {
	if s.nower == nil {
		return time.Now().UTC()
	}
	return s.nower().UTC()
}

// resetActions adjusts all the actions to their initial un-started state.
// This is used by the ContChecks to reset the actions before each run.
func resetActions(actions []*workflow.Action) {
	for _, action := range actions {
		action.State.Set(workflow.State{Status: workflow.NotStarted})
		action.Attempts.Clear()
	}
}

func isType(a, b any) bool {
	return reflect.TypeOf(a) == reflect.TypeOf(b)
}

func after(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	t := time.NewTimer(d)
	defer t.Stop()

	// A timer that fired as ctx ended still means ctx ended, so only a clean receive lets the caller go on.
	if _, r := chans.Get(ctx, t.C); r != chans.ResultOK {
		return ctx.Err()
	}
	return nil
}
