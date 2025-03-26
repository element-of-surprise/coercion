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
	plan := req.Data.Plan

	fixPlan(plan)
	switch plan.State.Status {
	case workflow.NotStarted:
		req.Next = s.Start
		return req
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		req.Next = s.End
		return req
	}
	// Okay, we are in the running state. Let's setup to run.

	req.Ctx = context.SetPlanID(req.Ctx, req.Data.Plan.ID)

	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)

	if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}

	req.Next = s.PlanBypassChecks
	return req
}

type stater interface {
	GetState() *workflow.State
	SetState(*workflow.State)
}

func fixAction(a *workflow.Action) {
	if a.State.Status != workflow.Running {
		return
	}
	if len(a.Attempts) == 0 {
		resetAction(a)
	}
}

func resetAction(a *workflow.Action) {
	a.State.Status = workflow.NotStarted
	a.State.Start = time.Time{}
	a.State.End = time.Time{}
	a.Attempts = nil
}

// fixChecks looks at a Checks object and if it is in the Running state, resets it as
// if it had not started. All actions are reset as well.
func fixChecks(c *workflow.Checks) {
	if c == nil {
		return
	}
	if c.State.Status != workflow.Running {
		return
	}

	c.State.Status = workflow.NotStarted
	c.State.Start = time.Time{}
	c.State.End = time.Time{}

	for _, a := range c.Actions {
		resetAction(a)
	}
}

func fixSeq(s *workflow.Sequence) {
	if s.State.Status != workflow.Running {
		return
	}

	stopped := 0
	for _, a := range s.Actions {
		if a.State.Status == workflow.Stopped {
			stopped++
		}
	}
	if stopped > 0 {
		for _, a := range s.Actions {
			if a.State.Status == workflow.Running {
				a.State.Status = workflow.Stopped
				a.State.End = time.Now()
			}
		}
		s.State.Status = workflow.Stopped
		s.State.End = time.Now()
		return
	}

	completed := 0
	running := 0
	for _, a := range s.Actions {
		fixAction(a)
		switch a.State.Status {
		case workflow.Completed:
			completed++
		case workflow.Running:
			running++
		case workflow.Stopped:
			stopped++
		}
	}
	if stopped > 0 {
		for _, a := range s.Actions {
			if a.State.Status == workflow.Running {
				a.State.Status = workflow.Stopped
				a.State.End = time.Now()
			}
		}
		s.State.Status = workflow.Stopped
		s.State.End = time.Now()
		return
	}
	if completed == 0 && running == 0 {
		s.State.Status = workflow.NotStarted
		s.State.Start = time.Time{}
		s.State.End = time.Time{}
		return
	}
	if completed == len(s.Actions) {
		s.State.Status = workflow.Completed
		s.State.End = time.Now()
		return
	}
}

func fixBlock(b *workflow.Block) {
	if b.State.Status != workflow.Running {
		return
	}
	if b.BypassChecks != nil {
		fixChecks(b.BypassChecks)
		if b.BypassChecks.State.Status == workflow.Completed {
			b.State.Status = workflow.Completed
			return
		}
	}
	if b.PreChecks != nil {
		if b.PreChecks.State.Status == workflow.Failed {
			b.State.Status = workflow.Failed
			return
		}
		fixChecks(b.PreChecks)
	}
	if b.ContChecks != nil {
		if b.ContChecks.State.Status == workflow.Failed {
			b.State.Status = workflow.Failed
			return
		}
		fixChecks(b.ContChecks)
	}
	if b.PostChecks != nil {
		if b.PostChecks.State.Status == workflow.Failed {
			b.State.Status = workflow.Failed
			return
		}
		fixChecks(b.PostChecks)
	}

	running := 0
	completed := 0
	failed := 0
	stopped := 0
	for _, s := range b.Sequences {
		fixSeq(s)
		switch s.State.Status {
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
		for _, s := range b.Sequences {
			if s.State.Status == workflow.Running {
				s.State.Status = workflow.Stopped
			}
		}
		b.State.Status = workflow.Stopped
		return
	}
	if completed == 0 && running == 0 && failed == 0 {
		b.State.Status = workflow.NotStarted
		b.State.Start = time.Time{}
		b.State.End = time.Time{}
		return
	}
	if completed == len(b.Sequences) && b.PostChecks == nil || b.PostChecks.State.Status == workflow.Completed {
		b.State.Status = workflow.Completed
		b.State.End = time.Now()
		return
	}
	if running == 0 && completed+failed == len(b.Sequences) {
		if failed <= b.ToleratedFailures {
			b.State.Status = workflow.Completed
			b.State.End = time.Now()
			return
		}
		b.State.Status = workflow.Failed
		b.State.End = time.Now()
		return
	}
}

func fixPlan(p *workflow.Plan) {
	if p.State.Status != workflow.Running {
		return
	}
	if p.BypassChecks != nil {
		fixChecks(p.BypassChecks)
		if checksCompleted(p.BypassChecks) {
			p.State.Status = workflow.Completed
			return
		}
	}
	if checksFailed(p.PreChecks) {
		p.State.Status = workflow.Failed
		return
	}
	fixChecks(p.PreChecks)

	if checksFailed(p.PostChecks) {
		p.State.Status = workflow.Failed
		return
	}
	fixChecks(p.PostChecks)

	if checksFailed(p.ContChecks) {
		p.State.Status = workflow.Failed
	}
	fixChecks(p.ContChecks)

	if p.DeferredChecks != nil {
		fixChecks(p.DeferredChecks)
	}

	running := 0
	completed := 0
	failed := 0
	for _, b := range p.Blocks {
		fixBlock(b)
		if b.State.Status == workflow.Stopped {
			p.State.Status = workflow.Stopped
			return
		}
		switch b.State.Status {
		case workflow.Completed:
			completed++
		case workflow.Running:
			running++
		case workflow.Failed:
			failed++
		}
	}
	if failed > 0 {
		p.State.Status = workflow.Failed
		p.State.End = time.Now()
		return
	}
	if completed == 0 && running == 0 && failed == 0 {
		p.State.Status = workflow.NotStarted
		p.State.Start = time.Time{}
		p.State.End = time.Time{}
		return
	}
	if completed == len(p.Blocks) {
		if checksCompleted(p.PostChecks) && checksCompleted(p.DeferredChecks) {
			p.State.Status = workflow.Completed
			p.State.End = time.Now()
			return
		}
	}
}

func checksFailed(c *workflow.Checks) bool {
	if c == nil {
		return false
	}
	if c.State.Status == workflow.Failed {
		return true
	}
	return false
}

func checksCompleted(c *workflow.Checks) bool {
	if c == nil {
		return true
	}
	if c.State.Status == workflow.Completed {
		return true
	}
	return false
}

func skipRecoveredChecks(c *workflow.Checks) bool {
	if c == nil {
		return true
	}
	if c.State.Status != workflow.NotStarted {
		return false
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
