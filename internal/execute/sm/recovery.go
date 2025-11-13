package sm

import (
	"sync/atomic"
	"time"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/context"
	"github.com/gostdlib/base/statemachine"
	"github.com/gostdlib/base/telemetry/log"
	"github.com/kylelemons/godebug/pretty"
)

// Recovery restarts execution of a Plan that has already started running, but the service crashed before it completed.
func (s *States) Recovery(req statemachine.Request[Data]) statemachine.Request[Data] {
	log.Println("recovery state started")
	defer func() {
		log.Println("recovery state completed")
		if req.Data.RecoveryStarted != nil {
			log.Println("closing recovery started channel")
			close(req.Data.RecoveryStarted)
		}
	}()

	plan := req.Data.Plan
	req.Data.recovered = true

	log.Printf("fixing plan for recovery: %s|%s", plan.ID, plan.GetState().Status)
	s.fixPlan(plan)
	if err := s.store.UpdatePlan(req.Ctx, plan); err != nil {
		log.Fatalf("failed to write Plan: %v", err)
	}
	log.Printf("plan fixed for recovery: %s|%s", plan.ID, plan.GetState().Status)
	log.Println("===========================Fix Plan Complete===========================")
	switch plan.State.Status {
	case workflow.NotStarted:
		req.Next = nil
		log.Println("plan not started, not recovering")
		return req
	case workflow.Completed, workflow.Failed, workflow.Stopped:
		req.Next = s.End
		log.Println("plan already completed, failed, or stopped, moving to end state")
		return req
	}
	// Okay, we are in the running state. Let's setup to run.

	req.Ctx = context.SetPlanID(req.Ctx, req.Data.Plan.ID)

	// Setup our internal block objects that are used to track the state of the blocks.
	for _, b := range req.Data.Plan.Blocks {
		req.Data.blocks = append(req.Data.blocks, block{block: b, contCheckResult: make(chan error, 1)})
	}
	req.Data.contCheckResult = make(chan error, 1)
	log.Println("plan recovery setup complete, updating plan in store")

	var pConfig = pretty.Config{
		IncludeUnexported: false,
		PrintStringers:    true,
		SkipZeroFields:    true,
	}

	log.Println("plan before we run again: ", pConfig.Sprint("Plan in vault: \n", req.Data.Plan))

	req.Next = s.PlanBypassChecks
	log.Println("moving to plan bypass checks state")
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
		return
	}
	// We started to run, but didn't finish. Since we don't know the state, we just pretend it didn't happen.
	if a.Attempts[len(a.Attempts)-1].End.IsZero() {
		a.Attempts = a.Attempts[:len(a.Attempts)-1]
		fixAction(a)
		return
	}
	if a.Attempts[len(a.Attempts)-1].Err == nil {
		a.State.Status = workflow.Completed
		a.State.End = a.Attempts[len(a.Attempts)-1].End
		return
	}
	// Okay, this means we failed, so we need to set the state to failed.
	a.State.Status = workflow.Failed
	a.State.End = a.Attempts[len(a.Attempts)-1].End
}

func resetAction(a *workflow.Action) {
	a.State.Status = workflow.NotStarted
	a.State.Start = time.Time{}
	a.State.End = time.Time{}
	a.Attempts = nil
}

// fixChecks looks at a Checks object and if it is in the Running state (or has started),
// examines the action states and sets the Checks state accordingly.
func fixChecks(c *workflow.Checks) {
	if c == nil {
		return
	}

	if c.State.Status != workflow.Running {
		return
	}

	// First pass: check for stopped actions. We end up looping twice because we don't want to
	// fix actions if we are going to stop everything.
	stopped := 0
	for _, a := range c.Actions {
		if a.State.Status == workflow.Stopped {
			stopped++
		}
	}
	if stopped > 0 {
		for _, a := range c.Actions {
			if a.State.Status == workflow.Running {
				a.State.Status = workflow.Stopped
				a.State.End = time.Now()
			}
		}
		c.State.Status = workflow.Stopped
		c.State.End = time.Now()
		return
	}

	// Fix all actions and count their states
	completed := 0
	running := 0
	failed := 0
	for _, a := range c.Actions {
		fixAction(a)
		switch a.State.Status {
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
		c.State.Status = workflow.Stopped
		c.State.End = time.Now()
	case failed > 0:
		c.State.Status = workflow.Failed
		c.State.End = time.Now()
	case completed == 0 && running == 0 && failed == 0:
		c.State.Status = workflow.NotStarted
		c.State.Start = time.Time{}
		c.State.End = time.Time{}
	case completed == len(c.Actions):
		c.State.Status = workflow.Completed
		c.State.End = time.Now()
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
	failed := 0
	for _, a := range s.Actions {
		fixAction(a)
		switch a.State.Status {
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
		s.State.Status = workflow.Stopped
		s.State.End = time.Now()
	case failed > 0:
		s.State.Status = workflow.Failed
		s.State.End = time.Now()
	case completed == 0 && running == 0:
		s.State.Status = workflow.NotStarted
		s.State.Start = time.Time{}
		s.State.End = time.Time{}
	case completed == len(s.Actions):
		s.State.Status = workflow.Completed
		s.State.End = time.Now()
	}
}

func (s *States) fixBlock(b *workflow.Block) {
	log.Println("====fixBlock====")
	if b.State.Status != workflow.Running {
		return
	}
	log.Println("fixing bypass checks")
	if b.BypassChecks != nil {
		fixChecks(b.BypassChecks)
		if b.BypassChecks.State.Status == workflow.Completed {
			b.State.Status = workflow.Completed
			return
		}
	}
	log.Println("fixing pre checks")
	if b.PreChecks != nil {
		if b.PreChecks.State.Status == workflow.Failed {
			b.State.Status = workflow.Failed
			return
		}
		fixChecks(b.PreChecks)
	}
	log.Println("fixing cont checks")
	if b.ContChecks != nil {
		if b.ContChecks.State.Status == workflow.Failed {
			b.State.Status = workflow.Failed
			return
		}
		fixChecks(b.ContChecks)
	}
	log.Println("fixing post checks")
	if b.PostChecks != nil {
		if b.PostChecks.State.Status == workflow.Failed {
			b.State.Status = workflow.Failed
			return
		}
		fixChecks(b.PostChecks)
	}

	// DOAK - remove this before submit
	//g := context.Pool(context.Background()).Group()
	var completed, failed, stopped, running atomic.Int32
	// DOAK - remove this before submit
	//seqs := make([]*workflow.Sequence, 0)
	log.Println("fixing sequences")
	for _, seq := range b.Sequences {
		fixSeq(seq)
		switch seq.State.Status {
		case workflow.Completed:
			completed.Add(1)
		case workflow.Failed:
			failed.Add(1)
		case workflow.Stopped:
			stopped.Add(1)
		case workflow.Running:
			running.Add(1)
			// DOAK - remove this before submit
			/*
				seqs = append(seqs, seq)
				g.Go(
					context.Background(),
					func(ctx context.Context) error {
						log.Println("executing seq: ", seq.ID)
						err := s.execSeq(ctx, seq)
						log.Println("finished seq: ", seq.ID)
						switch seq.GetState().Status {
						case workflow.Completed:
							completed.Add(1)
						case workflow.Failed:
							failed.Add(1)
						case workflow.Stopped:
							stopped.Add(1)
						default:
							panic("unexpected seq state after execSeq: " + seq.GetState().Status.String())

						}
						return err
					},
				)
			*/
		}
	}
	// DOAK - remove this before submit
	/*
		log.Println("waiting for running sequences to complete")
		_ = g.Wait(context.Background())
		log.Println("all sequences fixed and running sequences completed")
	*/

	if stopped.Load() > 0 {
		for _, s := range b.Sequences {
			if s.State.Status == workflow.Running {
				s.State.Status = workflow.Stopped
			}
		}
		b.State.Status = workflow.Stopped
		return
	}

	switch {
	case completed.Load() == 0 && failed.Load() == 0 && running.Load() == 0:
		b.State.Status = workflow.NotStarted
		b.State.Start = time.Time{}
		b.State.End = time.Time{}
	}
}

func (s *States) fixPlan(p *workflow.Plan) {
	defer log.Println("plan fix complete")
	if p.State.Status != workflow.Running {
		return
	}
	log.Println("fixing BypassChecks")
	if p.BypassChecks != nil {
		fixChecks(p.BypassChecks)
		if checksCompleted(p.BypassChecks) {
			p.State.Status = workflow.Completed
			return
		}
	}
	log.Println("fixing PreChecks")
	if checksFailed(p.PreChecks) {
		p.State.Status = workflow.Failed
		return
	}
	fixChecks(p.PreChecks)

	log.Println("fixing PostChecks")
	if checksFailed(p.PostChecks) {
		p.State.Status = workflow.Failed
		return
	}
	fixChecks(p.PostChecks)

	log.Println("fixing ContChecks")
	if checksFailed(p.ContChecks) {
		p.State.Status = workflow.Failed
		return
	}
	fixChecks(p.ContChecks)

	log.Println("fixing DeferredChecks")
	if p.DeferredChecks != nil {
		fixChecks(p.DeferredChecks)
	}

	running := 0
	completed := 0
	failed := 0
	log.Println("fixing Blocks")
	for _, b := range p.Blocks {
		log.Println("fixing Block:", b.ID)
		s.fixBlock(b)
		log.Println("fixed Block:", b.ID, "Status:", b.State.Status)
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
	log.Println("evaluating plan state after fix")
	if failed > 0 {
		p.State.Status = workflow.Failed
		p.State.End = time.Now()
		return
	}
	if completed == 0 && running == 0 && failed == 0 {
		p.State.Start = time.Time{}
		p.State.End = time.Time{}
		return
	}
	log.Println("checking if plan is completed")
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

// skipRecoveredChecks returns if we should skip execution of recovered checks. This is only
// valid for PreChecks and BypassChecks. Returns true if checks have already completed execution.
func skipRecoveredChecks(c *workflow.Checks) bool {
	if c == nil {
		return true
	}
	// Skip checks that have already completed execution (including failures)
	if c.State.Status == workflow.Completed || c.State.Status == workflow.Failed || c.State.Status == workflow.Stopped {
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
