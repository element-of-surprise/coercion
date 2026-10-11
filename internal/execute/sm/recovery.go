package sm

import (
	"fmt"
	"slices"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
	"github.com/gostdlib/base/statemachine"
)

// Recovery restarts execution of a Plan that has already started running, but the service crashed before it completed.
func (s *States) Recovery(req statemachine.Request[Data]) statemachine.Request[Data] {
	context.Log(req.Ctx).Info("recovery state started")
	defer func() {
		context.Log(req.Ctx).Info("recovery state completed")
		if req.Data.RecoveryStarted != nil {
			req.Data.RecoveryStarted.Report(req.Err)
		}
	}()

	plan := req.Data.Plan
	req.Data.recovered = true

	wasRunning := plan.State.Get().Status == workflow.Running
	before := changes.Record(plan)
	cut := s.fixPlan(plan)
	// A live run runs the DeferredChecks of a block that failed, then the Plan's DeferredActions and DeferredChecks,
	// before End, so a recovered Plan that failed must finish them too.
	var blockDeferred *workflow.Block
	if plan.State.Get().Status == workflow.Failed {
		blockDeferred = unfinishedBlockDeferred(plan)
	}
	deferred := plan.State.Get().Status == workflow.Failed && (blockDeferred != nil || !deferredDone(plan))
	if st := plan.State.Get().Status; st == workflow.Failed || st == workflow.Stopped {
		var keep []workflow.Object
		if deferred {
			keep = resumedDeferred(plan, blockDeferred)
		}
		if cut.block != nil {
			keep = append(keep, cut.block)
		}
		walk.SettleRunningItems(recoveryObjects(plan, keep...), st, s.now())
	}
	// status is the Plan's state as fixPlan settled it. It only routes the run below: the Plan's final state and
	// reason are worked out from its objects by End, whose writeEverything is the only place a final Plan is written.
	status := plan.State.Get().Status
	if status != workflow.Running && (wasRunning || deferred) {
		// Store the Plan as Running until End works out its final state, as a live run does: recovery only resumes
		// Running Plans, so one stored as finished now would keep that state for good if the process stopped before
		// End wrote the real one, losing any deferred work left and the reason End would give.
		state := plan.State.Get()
		state.Status = workflow.Running
		state.End = time.Time{}
		plan.State.Set(state)
	}
	if err := s.store.UpdateChanges(req.Ctx, plan, before); err != nil {
		req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("recovery could not write Plan(%s) objects: %w", plan.ID, err))
		return req
	}
	if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
		req.Err = errors.E(req.Ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("recovery could not write Plan(%s): %w", plan.ID, err))
		return req
	}
	switch status {
	case workflow.NotStarted:
		req.Next = nil
		return req
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		if !deferred && cut.block == nil {
			// Nothing is left to run, so End works out and writes the Plan's final state.
			req.Next = s.End
			return req
		}
	}
	// Okay, we are in the running state. Let's setup to run.

	req.Ctx = context.SetPlanID(req.Ctx, req.Data.Plan.ID)
	req = s.startHeartbeat(req)

	if cut.block != nil {
		// FinishCutBlock finishes the block, then BlockDeferredChecks and BlockEnd take the Plan on to its deferred
		// work, as in a live run whose ContChecks failed.
		req.Data.blocks = []block{{block: cut.block, contCheckResult: make(chan error, 1)}}
		req.Data.cut = cut.actions
		req.Next = s.FinishCutBlock
		return req
	}

	if deferred {
		if blockDeferred != nil {
			// BlockDeferredChecks runs the failed block's DeferredChecks and BlockEnd then moves on to the Plan's
			// deferred work, as in a live run.
			req.Data.blocks = []block{{block: blockDeferred, contCheckResult: make(chan error, 1)}}
			req.Next = s.BlockDeferredChecks
			return req
		}
		// The Plan has failed, so only its deferred work is left. PlanDeferredActions picks the batches for a failed
		// Plan from the failed objects, as it does in a live run.
		req.Next = s.PlanDeferredActions
		return req
	}

	if blocksCompleted(plan) {
		// Every block is done, so no new loop of the Plan's ContChecks may start: go on to the PostChecks and the
		// deferred work, which PlanDeferredActions picks from what is stored, as a live run does after its last block.
		// PlanPostChecks runs a pass the crash cut short once more, where a live run drains it.
		req.Next = s.PlanPostChecks
		return req
	}

	// Setup our internal block objects that are used to track the state of the blocks.
	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)

	req.Next = s.PlanBypassChecks
	return req
}

func fixAction(a *workflow.Action) {
	if a.State.Get().Status != workflow.Running {
		return
	}
	attempts := a.Attempts.Get()
	if len(attempts) == 0 {
		resetAction(a)
		return
	}
	// We started to run, but didn't finish. Since we don't know the state, we just pretend it didn't happen.
	if attempts[len(attempts)-1].End.IsZero() {
		a.Attempts.Set(attempts[:len(attempts)-1])
		fixAction(a)
		return
	}
	last := attempts[len(attempts)-1]
	// The last attempt failed but the action had retries left, so the crash cut it between attempts. It stays Running
	// with its attempts, so the action runner retries it and counts them against its retries, as a live run would.
	if last.Err != nil && !last.Err.Permanent && len(attempts) <= a.Retries {
		return
	}
	if last.Err == nil {
		state := a.State.Get()
		state.Status = workflow.Completed
		state.End = attempts[len(attempts)-1].End
		a.State.Set(state)
		return
	}
	// Okay, this means we failed, so we need to set the state to failed.
	state := a.State.Get()
	state.Status = workflow.Failed
	state.End = attempts[len(attempts)-1].End
	a.State.Set(state)
}

// failRunning settles each action in actions that is still Running as Failed. A Checks object that another of its
// actions failed is settled Failed and not run again, so an action fixAction left Running to be retried would stay
// Running for good. It ends when its last attempt did, or now if it has none.
func failRunning(actions []*workflow.Action) {
	for _, a := range actions {
		state := a.State.Get()
		if state.Status != workflow.Running {
			continue
		}
		state.Status = workflow.Failed
		state.End = time.Now()
		if attempts := a.Attempts.Get(); len(attempts) > 0 {
			state.End = attempts[len(attempts)-1].End
		}
		a.State.Set(state)
	}
}

func resetAction(a *workflow.Action) {
	a.State.Set(workflow.State{Status: workflow.NotStarted})
	a.Attempts.Clear()
}

// fixChecks looks at a Checks object and if it is in the Running state (or has started),
// examines the action states and sets the Checks state accordingly.
func fixChecks(c *workflow.Checks) {
	if c == nil {
		return
	}

	if c.State.Get().Status != workflow.Running {
		return
	}

	// First pass: check for stopped actions. We end up looping twice because we don't want to
	// fix actions if we are going to stop everything.
	stopped := 0
	for _, a := range c.Actions {
		if a.State.Get().Status == workflow.Stopped {
			stopped++
		}
	}
	if stopped > 0 {
		for _, a := range c.Actions {
			if a.State.Get().Status == workflow.Running {
				state := a.State.Get()
				state.Status = workflow.Stopped
				state.End = time.Now()
				a.State.Set(state)
			}
		}
		state := c.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		c.State.Set(state)
		return
	}

	// Fix all actions and count their states
	completed := 0
	running := 0
	failed := 0
	for _, a := range c.Actions {
		fixAction(a)
		switch a.State.Get().Status {
		case workflow.Completed:
			completed++
		case workflow.Running:
			running++
		case workflow.Failed:
			failed++
		case workflow.Stopped:
			stopped++
		}
	}

	// Set Checks state based on action states
	switch {
	case stopped > 0:
		state := c.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		c.State.Set(state)
	case failed > 0:
		failRunning(c.Actions)
		state := c.State.Get()
		state.Status = workflow.Failed
		state.End = time.Now()
		c.State.Set(state)
	case completed == len(c.Actions):
		state := c.State.Get()
		state.Status = workflow.Completed
		state.End = time.Now()
		c.State.Set(state)
	default:
		// If we get here, checks are Running but haven't completed yet (or have no actions).
		// If there are no failures or stops, reset all actions and the checks to NotStarted
		// so they can run fresh after recovery.
		for _, a := range c.Actions {
			resetAction(a)
		}
		c.State.Set(workflow.State{Status: workflow.NotStarted})
	}
}

func fixSeq(s *workflow.Sequence) {
	if s.State.Get().Status != workflow.Running {
		return
	}

	stopped := 0
	for _, a := range s.Actions {
		if a.State.Get().Status == workflow.Stopped {
			stopped++
		}
	}
	if stopped > 0 {
		for _, a := range s.Actions {
			if a.State.Get().Status == workflow.Running {
				state := a.State.Get()
				state.Status = workflow.Stopped
				state.End = time.Now()
				a.State.Set(state)
			}
		}
		state := s.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		s.State.Set(state)
		return
	}

	completed := 0
	running := 0
	failed := 0
	for _, a := range s.Actions {
		fixAction(a)
		switch a.State.Get().Status {
		case workflow.Completed:
			completed++
		case workflow.Running:
			running++
		case workflow.Failed:
			failed++
		case workflow.Stopped:
			stopped++
		}
	}

	switch {
	case stopped > 0:
		state := s.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		s.State.Set(state)
	case failed > 0:
		state := s.State.Get()
		state.Status = workflow.Failed
		state.End = time.Now()
		s.State.Set(state)
	case completed == 0 && running == 0:
		s.State.Set(workflow.State{Status: workflow.NotStarted})
	case completed == len(s.Actions):
		state := s.State.Get()
		state.Status = workflow.Completed
		state.End = time.Now()
		s.State.Set(state)
	}
}

// fixDeferBatch is the DeferBatch analogue of fixSeq: it takes a batch that was
// Running at the point of a crash and resolves it based on the state of its
// Actions (which fixAction has already normalized).
func fixDeferBatch(b *workflow.DeferBatch) {
	if b.State.Get().Status != workflow.Running {
		return
	}

	stopped := 0
	for _, a := range b.Actions {
		if a.State.Get().Status == workflow.Stopped {
			stopped++
		}
	}
	if stopped > 0 {
		for _, a := range b.Actions {
			if a.State.Get().Status == workflow.Running {
				state := a.State.Get()
				state.Status = workflow.Stopped
				state.End = time.Now()
				a.State.Set(state)
			}
		}
		state := b.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		b.State.Set(state)
		return
	}

	completed := 0
	running := 0
	failed := 0
	for _, a := range b.Actions {
		fixAction(a)
		switch a.State.Get().Status {
		case workflow.Completed:
			completed++
		case workflow.Running:
			running++
		case workflow.Failed:
			failed++
		case workflow.Stopped:
			stopped++
		}
	}

	switch {
	case stopped > 0:
		state := b.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		b.State.Set(state)
	case failed > 0:
		state := b.State.Get()
		state.Status = workflow.Failed
		state.End = time.Now()
		b.State.Set(state)
	case completed == 0 && running == 0:
		b.State.Set(workflow.State{Status: workflow.NotStarted})
	case completed == len(b.Actions):
		state := b.State.Get()
		state.Status = workflow.Completed
		state.End = time.Now()
		b.State.Set(state)
	}
}

// fixDeferredActions resolves a DeferredActions container that was Running at
// crash time by fixing each of its batches and aggregating their outcomes.
func (s *States) fixDeferredActions(da *workflow.DeferredActions) {
	if da == nil {
		return
	}
	if da.State.Get().Status != workflow.Running {
		return
	}

	all := da.DeferredBatches

	stopped := 0
	for _, b := range all {
		if b.State.Get().Status == workflow.Stopped {
			stopped++
		}
	}
	if stopped > 0 {
		for _, b := range all {
			if b.State.Get().Status == workflow.Running {
				bs := b.State.Get()
				bs.Status = workflow.Stopped
				bs.End = time.Now()
				b.State.Set(bs)
			}
		}
		state := da.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		da.State.Set(state)
		return
	}

	stopped = 0
	var running, notStarted, failElementFailed int
	for _, b := range all {
		fixDeferBatch(b)
		switch b.State.Get().Status {
		case workflow.Running:
			running++
		case workflow.Failed:
			if b.FailElement {
				failElementFailed++
			}
		case workflow.Stopped:
			stopped++
		case workflow.NotStarted:
			notStarted++
		}
	}

	switch {
	case stopped > 0:
		state := da.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		da.State.Set(state)
	case running > 0 || notStarted > 0:
		// Work remains: batches were scheduled but never finished (or never started). Reset to NotStarted so
		// PlanDeferredActions re-enters and finishes. runDeferBatch short-circuits on already-terminal batches, and a
		// failed FailElement batch still fails the container once the rest are done. This comes before that failure:
		// a live run attempts every batch regardless of the others' failures, and a terminal container is not resumed.
		da.State.Set(workflow.State{Status: workflow.NotStarted})
	case failElementFailed > 0:
		state := da.State.Get()
		state.Status = workflow.Failed
		state.End = time.Now()
		da.State.Set(state)
	default:
		// All batches terminal: Completed or non-FailElement Failed.
		state := da.State.Get()
		state.Status = workflow.Completed
		state.End = time.Now()
		da.State.Set(state)
	}
}

// deferredActionsTerminal reports whether the DeferredActions container (or nil)
// is in a terminal state for fixPlan's purposes.
func deferredActionsTerminal(da *workflow.DeferredActions) bool {
	if da == nil {
		return true
	}
	switch da.State.Get().Status {
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		return true
	}
	return false
}

func (s *States) fixBlock(b *workflow.Block) {
	switch b.State.Get().Status {
	case workflow.Running:
	case workflow.Failed:
		// A block is written Failed before its DeferredChecks run, so they may have been cut short.
		fixChecks(b.DeferredChecks)
		return
	default:
		return
	}
	if b.BypassChecks != nil {
		fixChecks(b.BypassChecks)
		if b.BypassChecks.State.Get().Status == workflow.Completed {
			state := b.State.Get()
			state.Status = workflow.Completed
			b.State.Set(state)
			return
		}
	}
	// Settle each Checks from its actions before asking whether it failed, as fixPlan does: Checks still Running over
	// a Failed action have failed, and missing that would leave the block Running to re-run them on resume. That
	// includes the DeferredChecks: a run writes them Failed before it writes the block Failed, and on resume
	// BlockDeferredChecks resets and re-runs any that are not Completed, so a pass would complete a failed block.
	for _, c := range []*workflow.Checks{b.PreChecks, b.ContChecks, b.PostChecks, b.DeferredChecks} {
		fixChecks(c)
		if checksFailed(c) {
			state := b.State.Get()
			state.Status = workflow.Failed
			b.State.Set(state)
			fixChecks(b.DeferredChecks)
			return
		}
	}

	var completed, failed, stopped, running int
	for _, seq := range b.Sequences {
		fixSeq(seq)
		switch seq.State.Get().Status {
		case workflow.Completed:
			completed++
		case workflow.Failed:
			failed++
		case workflow.Stopped:
			stopped++
		case workflow.Running:
			running++
		}
	}

	if stopped > 0 {
		for _, seq := range b.Sequences {
			if seq.State.Get().Status == workflow.Running {
				state := seq.State.Get()
				state.Status = workflow.Stopped
				state.End = time.Now()
				seq.State.Set(state)
			}
		}
		state := b.State.Get()
		state.Status = workflow.Stopped
		state.End = time.Now()
		b.State.Set(state)
		return
	}

	switch {
	case completed == 0 && failed == 0 && running == 0:
		b.State.Set(workflow.State{Status: workflow.NotStarted})
	}
}

// fixPlan settles a recovered Running Plan from what is stored. If the Plan's ContChecks had failed, it returns the
// block they cut short for recovery to finish; otherwise the returned cutBlock is empty.
func (s *States) fixPlan(p *workflow.Plan) cutBlock {
	if p.State.Get().Status != workflow.Running {
		return cutBlock{}
	}
	if p.BypassChecks != nil {
		fixChecks(p.BypassChecks)
		if checksCompleted(p.BypassChecks) {
			state := p.State.Get()
			state.Status = workflow.Completed
			p.State.Set(state)
			return cutBlock{}
		}
	}
	// The deferred work runs after any failure below, so it is fixed before fixPlan can return on one. Otherwise a crash
	// part way through it would leave it Running, to be settled as Failed instead of resumed.
	fixChecks(p.DeferredChecks)
	s.fixDeferredActions(p.DeferredActions)
	// Settle each Checks from its actions before asking whether it failed: Checks still Running over a Failed action
	// have failed, and missing that would let the Plan be recovered as Completed.
	fixChecks(p.PreChecks)
	if checksFailed(p.PreChecks) {
		state := p.State.Get()
		state.Status = workflow.Failed
		p.State.Set(state)
		return cutBlock{}
	}

	fixChecks(p.PostChecks)
	if checksFailed(p.PostChecks) {
		state := p.State.Get()
		state.Status = workflow.Failed
		p.State.Set(state)
		return cutBlock{}
	}

	fixChecks(p.ContChecks)
	if checksFailed(p.ContChecks) {
		state := p.State.Get()
		state.Status = workflow.Failed
		p.State.Set(state)
		return fixCutBlock(p)
	}

	running := 0
	completed := 0
	failed := 0
	for _, b := range p.Blocks {
		s.fixBlock(b)
		if b.State.Get().Status == workflow.Stopped {
			state := p.State.Get()
			state.Status = workflow.Stopped
			p.State.Set(state)
			return cutBlock{}
		}
		switch b.State.Get().Status {
		case workflow.Completed:
			completed++
		case workflow.Running:
			running++
		case workflow.Failed:
			failed++
		}
	}
	if failed > 0 {
		state := p.State.Get()
		state.Status = workflow.Failed
		state.End = time.Now()
		p.State.Set(state)
		return cutBlock{}
	}
	// A ContChecks pass the crash cut short was reset to NotStarted and PlanPostChecks runs it again, so the Plan is not
	// done while one is left.
	if completed == len(p.Blocks) && !contPassCut(p.ContChecks) {
		if checksCompleted(p.PostChecks) && checksCompleted(p.DeferredChecks) && deferredActionsTerminal(p.DeferredActions) {
			state := p.State.Get()
			state.Status = workflow.Completed
			state.End = time.Now()
			p.State.Set(state)
			return cutBlock{}
		}
	}
	return cutBlock{}
}

// cutBlock is the block that was running when the Plan's ContChecks failed and the crash cut short.
type cutBlock struct {
	block *workflow.Block
	// actions are the actions in block the crash cut mid-attempt, which recovery runs again to completion.
	actions []cutAction
}

// fixCutAction settles an action Running at the crash in the block fixCutBlock fixes. Unlike fixAction, an action cut
// mid-attempt stays Running, with its unfinished attempt dropped, rather than going back to NotStarted. Nothing new
// starts in that block, so Running is the only stored mark that the action is to run again: a Recovery that ends on a
// failed write before FinishCutBlock runs it is retried, and the retry must still find it. An action whose last attempt
// finished is settled by fixAction.
func fixCutAction(a *workflow.Action) {
	if a.State.Get().Status != workflow.Running {
		return
	}
	attempts := a.Attempts.Get()
	if n := len(attempts); n > 0 && attempts[n-1].End.IsZero() {
		attempts = attempts[:n-1]
		// Set on an empty slice would store an empty Attempts rather than none.
		if len(attempts) == 0 {
			a.Attempts.Clear()
		} else {
			a.Attempts.Set(attempts)
		}
	}
	if len(attempts) == 0 {
		return
	}
	fixAction(a)
}

// cutAction is an action the crash cut mid-attempt and the sequence it is in.
type cutAction struct {
	seq    *workflow.Sequence
	action *workflow.Action
}

// fixCutBlock fixes the Running block of a Plan whose ContChecks failed, for FinishCutBlock to finish. Only one block
// runs at a time. Each action Running at the crash is settled from its attempts by fixCutAction: one whose last attempt
// finished keeps that outcome, and one cut mid-attempt or between retries stays Running and is returned to run again.
// The ContChecks are not run again and keep their last outcome (see fixCutContChecks). A PostChecks pass the crash cut
// short is reset to NotStarted to run again. The bypass and pre checks are settled Failed as they would be under any
// failed Plan: they run before the sequences, so one still Running means no sequence started.
func fixCutBlock(p *workflow.Plan) cutBlock {
	for _, b := range p.Blocks {
		if b.State.Get().Status != workflow.Running {
			continue
		}
		var items []walk.Item
		for _, c := range []*workflow.Checks{b.BypassChecks, b.PreChecks} {
			if c == nil {
				continue
			}
			items = append(items, walk.Item{Value: c})
			for _, a := range c.Actions {
				items = append(items, walk.Item{Value: a})
			}
		}
		walk.SettleRunningItems(slices.Values(items), workflow.Failed, time.Now())
		fixCutContChecks(b.ContChecks)
		fixChecks(b.PostChecks)
		fixChecks(b.DeferredChecks)

		var cut []cutAction
		for _, seq := range b.Sequences {
			if seq.State.Get().Status != workflow.Running {
				continue
			}
			stopped := slices.ContainsFunc(seq.Actions, func(a *workflow.Action) bool { return a.State.Get().Status == workflow.Stopped })
			n := len(cut)
			for _, a := range seq.Actions {
				if stopped || a.State.Get().Status != workflow.Running {
					continue
				}
				fixCutAction(a)
				if a.State.Get().Status == workflow.Running {
					cut = append(cut, cutAction{seq: seq, action: a})
				}
			}
			// A sequence with an action to run again stays Running until FinishCutBlock ends it.
			if len(cut) == n {
				fixSeq(seq)
			}
		}
		return cutBlock{block: b, actions: cut}
	}
	return cutBlock{}
}

// fixCutContChecks settles the ContChecks of the block a crash cut short after the Plan's ContChecks failed. They are
// not run again, so they keep their last outcome. A pass the crash cut short failed if an action in it has failed.
// Otherwise it ends Completed, the outcome of the passes before it (which passed, or the block would have failed),
// with its unfinished actions reset to NotStarted.
func fixCutContChecks(c *workflow.Checks) {
	if c == nil || c.State.Get().Status != workflow.Running {
		return
	}
	if slices.ContainsFunc(c.Actions, func(a *workflow.Action) bool { return a.State.Get().Status == workflow.Stopped }) {
		fixChecks(c)
		return
	}
	status := workflow.Completed
	for _, a := range c.Actions {
		fixAction(a)
		if a.State.Get().Status == workflow.Failed {
			status = workflow.Failed
		}
	}
	switch status {
	case workflow.Completed:
		for _, a := range c.Actions {
			if a.State.Get().Status != workflow.Completed {
				resetAction(a)
			}
		}
	case workflow.Failed:
		failRunning(c.Actions)
	}
	state := c.State.Get()
	state.Status = status
	state.End = time.Now()
	c.State.Set(state)
}

// contPassCut reports whether c holds a ContChecks pass that the crash cut short: fixChecks resets a Running pass with
// no failed or stopped action to NotStarted. Once every block is done, this is the pass a live run would drain in
// PlanPostChecks, so recovery runs it again there. A pass that had failed was settled Failed and is not cut.
func contPassCut(c *workflow.Checks) bool {
	return c != nil && c.State.Get().Status == workflow.NotStarted
}

// blocksCompleted reports whether every block of the Plan has completed.
func blocksCompleted(p *workflow.Plan) bool {
	for _, b := range p.Blocks {
		if b.State.Get().Status != workflow.Completed {
			return false
		}
	}
	return len(p.Blocks) > 0
}

// unfinishedBlockDeferred returns the first block of a failed Plan whose DeferredChecks a live run would still run,
// or nil. A live run runs a block's DeferredChecks after any failure in it, and a block still Running is settled
// Failed with the Plan. Only one block runs at a time, so at most one has DeferredChecks left.
func unfinishedBlockDeferred(p *workflow.Plan) *workflow.Block {
	for _, b := range p.Blocks {
		switch b.State.Get().Status {
		case workflow.Failed, workflow.Running:
		default:
			continue
		}
		if b.DeferredChecks == nil {
			continue
		}
		// The Plan may have failed before fixPlan reached this block, so its DeferredChecks may not be fixed yet.
		fixChecks(b.DeferredChecks)
		if b.DeferredChecks.State.Get().Status == workflow.NotStarted {
			return b
		}
	}
	return nil
}

// resumedDeferred returns the deferred work of a failed Plan that recovery resumes, which must not be settled: the
// failed block's DeferredChecks (if any) and the Plan's DeferredActions and DeferredChecks that are not terminal.
func resumedDeferred(p *workflow.Plan, blockDeferred *workflow.Block) []workflow.Object {
	var keep []workflow.Object
	if blockDeferred != nil {
		keep = append(keep, blockDeferred.DeferredChecks)
	}
	if p.DeferredActions != nil && !isCompleted(p.DeferredActions) {
		keep = append(keep, p.DeferredActions)
	}
	if p.DeferredChecks != nil && !isCompleted(p.DeferredChecks) {
		keep = append(keep, p.DeferredChecks)
	}
	return keep
}

// deferredDone reports whether the Plan's DeferredActions and DeferredChecks have nothing left to run.
func deferredDone(p *workflow.Plan) bool {
	return deferredActionsTerminal(p.DeferredActions) && (p.DeferredChecks == nil || isCompleted(p.DeferredChecks))
}

func checksFailed(c *workflow.Checks) bool {
	if c == nil {
		return false
	}
	if c.State.Get().Status == workflow.Failed {
		return true
	}
	return false
}

func checksCompleted(c *workflow.Checks) bool {
	if c == nil {
		return true
	}
	if c.State.Get().Status == workflow.Completed {
		return true
	}
	return false
}

// skipRecoveredChecks returns if we should skip execution of recovered checks. This is only
// valid for PreChecks and BypassChecks. Returns true if checks have already completed execution.
func skipRecoveredChecks(c *workflow.Checks) bool {
	if c == nil {
		return true
	}
	// Skip checks that have already completed execution (including failures)
	status := c.State.Get().Status
	if status == workflow.Completed || status == workflow.Failed || status == workflow.Stopped {
		return true
	}
	return false
}

// skipBlock returns true if the block is completed and should be skipped.
// This does not cover all states, because the Plan should have been fixed before this is called.
func skipBlock(b block) bool {
	return isCompleted(b.block)
}

// isCompleted returns true if the status is one of the completed states.
func isCompleted(o workflow.Object) bool {
	if o == nil {
		return false
	}
	state, ok := o.(walk.Stateful)
	if !ok { // The o can have nil in it.
		return false
	}
	switch state.GetState().Status {
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		return true
	}
	return false
}
