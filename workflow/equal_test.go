package workflow

import (
	"testing"
	"time"

	"github.com/element-of-surprise/coercion/plugins"
)

func TestStateEqual(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name string
		s1   *State
		s2   *State
		want bool
	}{
		{
			name: "Success: both nil",
			s1:   nil,
			s2:   nil,
			want: true,
		},
		{
			name: "Success: same pointer",
			s1: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag1",
			},
			s2: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag1",
			},
			want: true,
		},
		{
			name: "Success: equal values",
			s1: &State{
				Status: Completed,
				Start:  now,
				End:    later,
				ETag:   "etag2",
			},
			s2: &State{
				Status: Completed,
				Start:  now,
				End:    later,
				ETag:   "etag2",
			},
			want: true,
		},
		{
			name: "Success: different Status",
			s1: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag",
			},
			s2: &State{
				Status: Completed,
				Start:  now,
				End:    later,
				ETag:   "etag",
			},
			want: false,
		},
		{
			name: "Success: different Start",
			s1: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag",
			},
			s2: &State{
				Status: Running,
				Start:  later,
				End:    later,
				ETag:   "etag",
			},
			want: false,
		},
		{
			name: "Success: different End",
			s1: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag",
			},
			s2: &State{
				Status: Running,
				Start:  now,
				End:    now,
				ETag:   "etag",
			},
			want: false,
		},
		{
			name: "Success: different ETag",
			s1: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag1",
			},
			s2: &State{
				Status: Running,
				Start:  now,
				End:    later,
				ETag:   "etag2",
			},
			want: false,
		},
		{
			name: "Success: first nil",
			s1:   nil,
			s2: &State{
				Status: Running,
			},
			want: false,
		},
		{
			name: "Success: second nil",
			s1: &State{
				Status: Running,
			},
			s2:   nil,
			want: false,
		},
	}

	for _, test := range tests {
		got := test.s1.Equal(test.s2)
		if got != test.want {
			t.Errorf("TestStateEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestAttemptEqual(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name string
		a1   Attempt
		a2   Attempt
		want bool
	}{
		{
			name: "Success: both empty",
			a1:   Attempt{},
			a2:   Attempt{},
			want: true,
		},
		{
			name: "Success: equal values",
			a1: Attempt{
				Resp: "response",
				Err: &plugins.Error{
					Code:      1,
					Message:   "error",
					Permanent: true,
				},
				Start: now,
				End:   later,
			},
			a2: Attempt{
				Resp: "response",
				Err: &plugins.Error{
					Code:      1,
					Message:   "error",
					Permanent: true,
				},
				Start: now,
				End:   later,
			},
			want: true,
		},
		{
			name: "Success: different Resp",
			a1: Attempt{
				Resp:  "response1",
				Start: now,
				End:   later,
			},
			a2: Attempt{
				Resp:  "response2",
				Start: now,
				End:   later,
			},
			want: false,
		},
		{
			name: "Success: different Err",
			a1: Attempt{
				Resp: "response",
				Err: &plugins.Error{
					Code:    1,
					Message: "error1",
				},
				Start: now,
				End:   later,
			},
			a2: Attempt{
				Resp: "response",
				Err: &plugins.Error{
					Code:    1,
					Message: "error2",
				},
				Start: now,
				End:   later,
			},
			want: false,
		},
		{
			name: "Success: wrapped errors equal",
			a1: Attempt{
				Err: &plugins.Error{
					Code:    1,
					Message: "outer",
					Wrapped: &plugins.Error{
						Code:    2,
						Message: "inner",
					},
				},
			},
			a2: Attempt{
				Err: &plugins.Error{
					Code:    1,
					Message: "outer",
					Wrapped: &plugins.Error{
						Code:    2,
						Message: "inner",
					},
				},
			},
			want: true,
		},
		{
			name: "Success: wrapped errors different",
			a1: Attempt{
				Err: &plugins.Error{
					Code:    1,
					Message: "outer",
					Wrapped: &plugins.Error{
						Code:    2,
						Message: "inner1",
					},
				},
			},
			a2: Attempt{
				Err: &plugins.Error{
					Code:    1,
					Message: "outer",
					Wrapped: &plugins.Error{
						Code:    2,
						Message: "inner2",
					},
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.a1.Equal(test.a2)
		if got != test.want {
			t.Errorf("TestAttemptEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestActionEqual(t *testing.T) {
	t.Parallel()

	id1 := NewV7()
	id2 := NewV7()
	key1 := NewV7()
	key2 := NewV7()

	// Create actions with State for "equal values" test
	a1EqualValues := &Action{
		ID:      id1,
		Key:     key1,
		Name:    "action1",
		Descr:   "description",
		Plugin:  "plugin1",
		Timeout: 30 * time.Second,
		Retries: 3,
		Req:     "request",
	}
	a1EqualValues.Attempts.Set([]Attempt{{Resp: "resp1"}})
	a1EqualValues.State.Set(State{Status: Running})

	a2EqualValues := &Action{
		ID:      id1,
		Key:     key1,
		Name:    "action1",
		Descr:   "description",
		Plugin:  "plugin1",
		Timeout: 30 * time.Second,
		Retries: 3,
		Req:     "request",
	}
	a2EqualValues.Attempts.Set([]Attempt{{Resp: "resp1"}})
	a2EqualValues.State.Set(State{Status: Running})

	// Create action with State for "nil State vs non-nil State" test
	a2WithState := &Action{
		ID: id1,
	}
	a2WithState.State.Set(State{Status: Running})

	tests := []struct {
		name string
		a1   *Action
		a2   *Action
		want bool
	}{
		{
			name: "Success: both nil",
			a1:   nil,
			a2:   nil,
			want: true,
		},
		{
			name: "Success: equal values",
			a1:   a1EqualValues,
			a2:   a2EqualValues,
			want: true,
		},
		{
			name: "Success: different ID",
			a1: &Action{
				ID:   id1,
				Name: "action",
			},
			a2: &Action{
				ID:   id2,
				Name: "action",
			},
			want: false,
		},
		{
			name: "Success: different Key",
			a1: &Action{
				ID:   id1,
				Key:  key1,
				Name: "action",
			},
			a2: &Action{
				ID:   id1,
				Key:  key2,
				Name: "action",
			},
			want: false,
		},
		{
			name: "Success: different Name",
			a1: &Action{
				ID:   id1,
				Name: "action1",
			},
			a2: &Action{
				ID:   id1,
				Name: "action2",
			},
			want: false,
		},
		{
			name: "Success: different Req",
			a1: &Action{
				ID:  id1,
				Req: "request1",
			},
			a2: &Action{
				ID:  id1,
				Req: "request2",
			},
			want: false,
		},
		{
			name: "Success: different Attempts",
			a1: &Action{
				ID: id1,
				Attempts: func() AtomicSlice[Attempt] {
					var attempts AtomicSlice[Attempt]
					attempts.Set([]Attempt{{Resp: "resp1"}})
					return attempts
				}(),
			},
			a2: &Action{
				ID: id1,
				Attempts: func() AtomicSlice[Attempt] {
					var attempts AtomicSlice[Attempt]
					attempts.Set([]Attempt{{Resp: "resp2"}})
					return attempts
				}(),
			},
			want: false,
		},
		{
			name: "Success: nil State vs non-nil State",
			a1: &Action{
				ID: id1,
			},
			a2:   a2WithState,
			want: false,
		},
		{
			name: "Success: first nil",
			a1:   nil,
			a2:   &Action{},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.a1.Equal(test.a2)
		if got != test.want {
			t.Errorf("TestActionEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestSequenceEqual(t *testing.T) {
	t.Parallel()

	id1 := NewV7()
	id2 := NewV7()
	key1 := NewV7()

	// Create sequences with State for "equal values" test
	s1EqualValues := &Sequence{
		ID:    id1,
		Key:   key1,
		Name:  "sequence1",
		Descr: "description",
		Actions: []*Action{
			{ID: id1, Name: "action1"},
		},
	}
	s1EqualValues.State.Set(State{Status: Running})

	s2EqualValues := &Sequence{
		ID:    id1,
		Key:   key1,
		Name:  "sequence1",
		Descr: "description",
		Actions: []*Action{
			{ID: id1, Name: "action1"},
		},
	}
	s2EqualValues.State.Set(State{Status: Running})

	tests := []struct {
		name string
		s1   *Sequence
		s2   *Sequence
		want bool
	}{
		{
			name: "Success: both nil",
			s1:   nil,
			s2:   nil,
			want: true,
		},
		{
			name: "Success: equal values",
			s1:   s1EqualValues,
			s2:   s2EqualValues,
			want: true,
		},
		{
			name: "Success: different ID",
			s1: &Sequence{
				ID:   id1,
				Name: "sequence",
			},
			s2: &Sequence{
				ID:   id2,
				Name: "sequence",
			},
			want: false,
		},
		{
			name: "Success: different Actions",
			s1: &Sequence{
				ID: id1,
				Actions: []*Action{
					{ID: id1, Name: "action1"},
				},
			},
			s2: &Sequence{
				ID: id1,
				Actions: []*Action{
					{ID: id1, Name: "action2"},
				},
			},
			want: false,
		},
		{
			name: "Success: different Actions length",
			s1: &Sequence{
				ID: id1,
				Actions: []*Action{
					{ID: id1},
				},
			},
			s2: &Sequence{
				ID: id1,
				Actions: []*Action{
					{ID: id1},
					{ID: id2},
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.s1.Equal(test.s2)
		if got != test.want {
			t.Errorf("TestSequenceEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestChecksEqual(t *testing.T) {
	t.Parallel()

	id1 := NewV7()
	id2 := NewV7()
	key1 := NewV7()

	// Create checks with State for "equal values" test
	c1EqualValues := &Checks{
		ID:    id1,
		Key:   key1,
		Delay: 30 * time.Second,
		Actions: []*Action{
			{ID: id1, Name: "action1"},
		},
	}
	c1EqualValues.State.Set(State{Status: Running})

	c2EqualValues := &Checks{
		ID:    id1,
		Key:   key1,
		Delay: 30 * time.Second,
		Actions: []*Action{
			{ID: id1, Name: "action1"},
		},
	}
	c2EqualValues.State.Set(State{Status: Running})

	tests := []struct {
		name string
		c1   *Checks
		c2   *Checks
		want bool
	}{
		{
			name: "Success: both nil",
			c1:   nil,
			c2:   nil,
			want: true,
		},
		{
			name: "Success: equal values",
			c1:   c1EqualValues,
			c2:   c2EqualValues,
			want: true,
		},
		{
			name: "Success: different ID",
			c1: &Checks{
				ID: id1,
			},
			c2: &Checks{
				ID: id2,
			},
			want: false,
		},
		{
			name: "Success: different Delay",
			c1: &Checks{
				ID:    id1,
				Delay: 30 * time.Second,
			},
			c2: &Checks{
				ID:    id1,
				Delay: 60 * time.Second,
			},
			want: false,
		},
		{
			name: "Success: different Actions",
			c1: &Checks{
				ID: id1,
				Actions: []*Action{
					{ID: id1, Name: "action1"},
				},
			},
			c2: &Checks{
				ID: id1,
				Actions: []*Action{
					{ID: id1, Name: "action2"},
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.c1.Equal(test.c2)
		if got != test.want {
			t.Errorf("TestChecksEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestBlockEqual(t *testing.T) {
	t.Parallel()

	id1 := NewV7()
	id2 := NewV7()
	key1 := NewV7()

	// Create blocks with State for "equal values" test
	b1EqualValues := &Block{
		ID:            id1,
		Key:           key1,
		Name:          "block1",
		Descr:         "description",
		EntranceDelay: 10 * time.Second,
		ExitDelay:     5 * time.Second,
		BypassChecks: &Checks{
			ID: id1,
		},
		PreChecks: &Checks{
			ID: id2,
		},
		Sequences: []*Sequence{
			{ID: id1, Name: "seq1"},
		},
		Concurrency:       2,
		ToleratedFailures: 1,
	}
	b1EqualValues.State.Set(State{Status: Running})

	b2EqualValues := &Block{
		ID:            id1,
		Key:           key1,
		Name:          "block1",
		Descr:         "description",
		EntranceDelay: 10 * time.Second,
		ExitDelay:     5 * time.Second,
		BypassChecks: &Checks{
			ID: id1,
		},
		PreChecks: &Checks{
			ID: id2,
		},
		Sequences: []*Sequence{
			{ID: id1, Name: "seq1"},
		},
		Concurrency:       2,
		ToleratedFailures: 1,
	}
	b2EqualValues.State.Set(State{Status: Running})

	tests := []struct {
		name string
		b1   *Block
		b2   *Block
		want bool
	}{
		{
			name: "Success: both nil",
			b1:   nil,
			b2:   nil,
			want: true,
		},
		{
			name: "Success: equal values",
			b1:   b1EqualValues,
			b2:   b2EqualValues,
			want: true,
		},
		{
			name: "Success: different ID",
			b1: &Block{
				ID:   id1,
				Name: "block",
			},
			b2: &Block{
				ID:   id2,
				Name: "block",
			},
			want: false,
		},
		{
			name: "Success: different Name",
			b1: &Block{
				ID:   id1,
				Name: "block1",
			},
			b2: &Block{
				ID:   id1,
				Name: "block2",
			},
			want: false,
		},
		{
			name: "Success: different EntranceDelay",
			b1: &Block{
				ID:            id1,
				EntranceDelay: 10 * time.Second,
			},
			b2: &Block{
				ID:            id1,
				EntranceDelay: 20 * time.Second,
			},
			want: false,
		},
		{
			name: "Success: different Concurrency",
			b1: &Block{
				ID:          id1,
				Concurrency: 1,
			},
			b2: &Block{
				ID:          id1,
				Concurrency: 2,
			},
			want: false,
		},
		{
			name: "Success: different ToleratedFailures",
			b1: &Block{
				ID:                id1,
				ToleratedFailures: 0,
			},
			b2: &Block{
				ID:                id1,
				ToleratedFailures: 1,
			},
			want: false,
		},
		{
			name: "Success: different Sequences",
			b1: &Block{
				ID: id1,
				Sequences: []*Sequence{
					{ID: id1, Name: "seq1"},
				},
			},
			b2: &Block{
				ID: id1,
				Sequences: []*Sequence{
					{ID: id1, Name: "seq2"},
				},
			},
			want: false,
		},
		{
			name: "Success: different BypassChecks",
			b1: &Block{
				ID: id1,
				BypassChecks: &Checks{
					ID: id1,
				},
			},
			b2: &Block{
				ID: id1,
				BypassChecks: &Checks{
					ID: id2,
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.b1.Equal(test.b2)
		if got != test.want {
			t.Errorf("TestBlockEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestPlanEqual(t *testing.T) {
	t.Parallel()

	id1 := NewV7()
	id2 := NewV7()
	groupID1 := NewV7()
	now := time.Now()

	tests := []struct {
		name string
		p1   func() *Plan
		p2   func() *Plan
		want bool
	}{
		{
			name: "Success: both nil",
			p1:   func() *Plan { return nil },
			p2:   func() *Plan { return nil },
			want: true,
		},
		{
			name: "Success: equal values",
			p1: func() *Plan {
				p := &Plan{
					ID:      id1,
					Name:    "plan1",
					Descr:   "description",
					GroupID: groupID1,
					Meta:    []byte("metadata"),
					BypassChecks: &Checks{
						ID: id1,
					},
					PreChecks: &Checks{
						ID: id2,
					},
					Blocks: []*Block{
						{ID: id1, Name: "block1"},
					},
					SubmitTime: now,
					Reason:     FRBlock,
				}
				p.State.Set(State{Status: Running})
				return p
			},
			p2: func() *Plan {
				p := &Plan{
					ID:      id1,
					Name:    "plan1",
					Descr:   "description",
					GroupID: groupID1,
					Meta:    []byte("metadata"),
					BypassChecks: &Checks{
						ID: id1,
					},
					PreChecks: &Checks{
						ID: id2,
					},
					Blocks: []*Block{
						{ID: id1, Name: "block1"},
					},
					SubmitTime: now,
					Reason:     FRBlock,
				}
				p.State.Set(State{Status: Running})
				return p
			},
			want: true,
		},
		{
			name: "Success: different ID",
			p1: func() *Plan {
				return &Plan{
					ID:   id1,
					Name: "plan",
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID:   id2,
					Name: "plan",
				}
			},
			want: false,
		},
		{
			name: "Success: different Name",
			p1: func() *Plan {
				return &Plan{
					ID:   id1,
					Name: "plan1",
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID:   id1,
					Name: "plan2",
				}
			},
			want: false,
		},
		{
			name: "Success: different Meta",
			p1: func() *Plan {
				return &Plan{
					ID:   id1,
					Meta: []byte("meta1"),
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID:   id1,
					Meta: []byte("meta2"),
				}
			},
			want: false,
		},
		{
			name: "Success: nil Meta vs empty Meta",
			p1: func() *Plan {
				return &Plan{
					ID:   id1,
					Meta: nil,
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID:   id1,
					Meta: []byte{},
				}
			},
			want: true, // bytes.Equal treats nil and empty as equal
		},
		{
			name: "Success: different Blocks",
			p1: func() *Plan {
				return &Plan{
					ID: id1,
					Blocks: []*Block{
						{ID: id1, Name: "block1"},
					},
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID: id1,
					Blocks: []*Block{
						{ID: id1, Name: "block2"},
					},
				}
			},
			want: false,
		},
		{
			name: "Success: different SubmitTime",
			p1: func() *Plan {
				return &Plan{
					ID:         id1,
					SubmitTime: now,
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID:         id1,
					SubmitTime: now.Add(time.Hour),
				}
			},
			want: false,
		},
		{
			name: "Success: different Reason",
			p1: func() *Plan {
				return &Plan{
					ID:     id1,
					Reason: FRBlock,
				}
			},
			p2: func() *Plan {
				return &Plan{
					ID:     id1,
					Reason: FRPreCheck,
				}
			},
			want: false,
		},
		{
			name: "Success: first nil",
			p1:   func() *Plan { return nil },
			p2: func() *Plan {
				return &Plan{}
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := test.p1().Equal(test.p2())
		if got != test.want {
			t.Errorf("TestPlanEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestEqualSliceHelpers(t *testing.T) {
	t.Parallel()

	id1 := NewV7()
	id2 := NewV7()

	tests := []struct {
		name string
		a    []*Block
		b    []*Block
		want bool
	}{
		{
			name: "Success: both empty",
			a:    []*Block{},
			b:    []*Block{},
			want: true,
		},
		{
			name: "Success: equal blocks",
			a: []*Block{
				{ID: id1, Name: "block1"},
				{ID: id2, Name: "block2"},
			},
			b: []*Block{
				{ID: id1, Name: "block1"},
				{ID: id2, Name: "block2"},
			},
			want: true,
		},
		{
			name: "Success: different length",
			a: []*Block{
				{ID: id1},
			},
			b: []*Block{
				{ID: id1},
				{ID: id2},
			},
			want: false,
		},
		{
			name: "Success: different blocks",
			a: []*Block{
				{ID: id1, Name: "block1"},
			},
			b: []*Block{
				{ID: id1, Name: "block2"},
			},
			want: false,
		},
		{
			name: "Success: nil in first slice",
			a: []*Block{
				nil,
			},
			b: []*Block{
				{ID: id1},
			},
			want: false,
		},
		{
			name: "Success: nil in both slices",
			a: []*Block{
				nil,
			},
			b: []*Block{
				nil,
			},
			want: true,
		},
	}

	for _, test := range tests {
		got := sliceOfObjectsEqual(test.a, test.b)
		if got != test.want {
			t.Errorf("blocksEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestPluginErrorEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    *plugins.Error
		b    *plugins.Error
		want bool
	}{
		{
			name: "Success: both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "Success: equal errors",
			a: &plugins.Error{
				Code:      1,
				Message:   "error",
				Permanent: true,
			},
			b: &plugins.Error{
				Code:      1,
				Message:   "error",
				Permanent: true,
			},
			want: true,
		},
		{
			name: "Success: different Code",
			a: &plugins.Error{
				Code:    1,
				Message: "error",
			},
			b: &plugins.Error{
				Code:    2,
				Message: "error",
			},
			want: false,
		},
		{
			name: "Success: different Message",
			a: &plugins.Error{
				Code:    1,
				Message: "error1",
			},
			b: &plugins.Error{
				Code:    1,
				Message: "error2",
			},
			want: false,
		},
		{
			name: "Success: different Permanent",
			a: &plugins.Error{
				Code:      1,
				Message:   "error",
				Permanent: true,
			},
			b: &plugins.Error{
				Code:      1,
				Message:   "error",
				Permanent: false,
			},
			want: false,
		},
		{
			name: "Success: equal wrapped errors",
			a: &plugins.Error{
				Code:    1,
				Message: "outer",
				Wrapped: &plugins.Error{
					Code:    2,
					Message: "inner",
				},
			},
			b: &plugins.Error{
				Code:    1,
				Message: "outer",
				Wrapped: &plugins.Error{
					Code:    2,
					Message: "inner",
				},
			},
			want: true,
		},
		{
			name: "Success: different wrapped errors",
			a: &plugins.Error{
				Code:    1,
				Message: "outer",
				Wrapped: &plugins.Error{
					Code:    2,
					Message: "inner1",
				},
			},
			b: &plugins.Error{
				Code:    1,
				Message: "outer",
				Wrapped: &plugins.Error{
					Code:    2,
					Message: "inner2",
				},
			},
			want: false,
		},
		{
			name: "Success: one nil wrapped",
			a: &plugins.Error{
				Code:    1,
				Message: "outer",
				Wrapped: nil,
			},
			b: &plugins.Error{
				Code:    1,
				Message: "outer",
				Wrapped: &plugins.Error{
					Code:    2,
					Message: "inner",
				},
			},
			want: false,
		},
	}

	for _, test := range tests {
		got := pluginErrorEqual(test.a, test.b)
		if got != test.want {
			t.Errorf("TestPluginErrorEqual(%s): got %v, want %v", test.name, got, test.want)
		}
	}
}
