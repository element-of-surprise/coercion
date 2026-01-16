package sm

import (
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/telemetry/log"
)

// Recovery restarts execution of a Plan that has already started running, but the service crashed before it completed.
func (s *States) Recovery(req statemachine.Request[Data]) statemachine.Request[Data] {
	context.Log(req.Ctx).Info("recovery state started")
	defer func() {
		context.Log(req.Ctx).Info("recovery state completed")
		if req.Data.RecoveryStarted != nil {
			close(req.Data.RecoveryStarted)
		}
	}()

	plan := req.Data.Plan
	req.Data.recovered = true

	s.fixPlan(plan)
	if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}
	switch plan.State.Get().Status {
	case workflow.NotStarted:
		req.Next = nil
		return req
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		req.Next = s.End
		return req
	}
	// Okay, we are in the running state. Let's setup to run.

	req.Ctx = context.SetPlanID(req.Ctx, req.Data.Plan.ID)

	// Setup our internal block objects that are used to track the state of the blocks.
	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)

	req.Next = s.PlanBypassChecks
	return req
}

type stater interface {
	GetState() workflow.State
	SetState(workflow.State)
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
	if attempts[len(attempts)-1].Err == nil {
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

func resetAction(a *workflow.Action) {
	a.State.Set(workflow.State{Status: workflow.NotStarted})
	a.Attempts.Set(nil)
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

func (s *States) fixBlock(b *workflow.Block) {
	if b.State.Get().Status != workflow.Running {
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
	if b.PreChecks != nil {
		if b.PreChecks.State.Get().Status == workflow.Failed {
			state := b.State.Get()
			state.Status = workflow.Failed
			b.State.Set(state)
			return
		}
		fixChecks(b.PreChecks)
	}
	if b.ContChecks != nil {
		if b.ContChecks.State.Get().Status == workflow.Failed {
			state := b.State.Get()
			state.Status = workflow.Failed
			b.State.Set(state)
			return
		}
		fixChecks(b.ContChecks)
	}
	if b.PostChecks != nil {
		if b.PostChecks.State.Get().Status == workflow.Failed {
			state := b.State.Get()
			state.Status = workflow.Failed
			b.State.Set(state)
			return
		}
		fixChecks(b.PostChecks)
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

func (s *States) fixPlan(p *workflow.Plan) {
	if p.State.Get().Status != workflow.Running {
		return
	}
	if p.BypassChecks != nil {
		fixChecks(p.BypassChecks)
		if checksCompleted(p.BypassChecks) {
			state := p.State.Get()
			state.Status = workflow.Completed
			p.State.Set(state)
			return
		}
	}
	if checksFailed(p.PreChecks) {
		state := p.State.Get()
		state.Status = workflow.Failed
		p.State.Set(state)
		return
	}
	fixChecks(p.PreChecks)

	if checksFailed(p.PostChecks) {
		state := p.State.Get()
		state.Status = workflow.Failed
		p.State.Set(state)
		return
	}
	fixChecks(p.PostChecks)

	if checksFailed(p.ContChecks) {
		state := p.State.Get()
		state.Status = workflow.Failed
		p.State.Set(state)
		return
	}
	fixChecks(p.ContChecks)

	if p.DeferredChecks != nil {
		fixChecks(p.DeferredChecks)
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
			return
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
		return
	}
	if completed == len(p.Blocks) {
		if checksCompleted(p.PostChecks) && checksCompleted(p.DeferredChecks) {
			state := p.State.Get()
			state.Status = workflow.Completed
			state.End = time.Now()
			p.State.Set(state)
			return
		}
	}
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
	state, ok := o.(stater)
	if !ok { // The o can have nil in it.
		return false
	}
	switch state.GetState().Status {
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		return true
	}
	return false
}
