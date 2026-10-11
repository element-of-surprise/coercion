package workflow

import (
	"testing"
	"time"

	"github.com/go-json-experiment/json"
)

// TestAtomicValueJSON verifies JSON round-tripping of AtomicValue. An unset value
// marshals to null; unmarshaling null must leave the value unset (nil pointer),
// symmetric with MarshalJSON. Otherwise a round-tripped object no longer matches its
// origin: a previously-unset (zero) field comes back materialized as a non-zero {}
// field, which is what broke plan equality after a storage round-trip.
func TestAtomicValueJSON(t *testing.T) {
	tests := []struct {
		name    string
		set     bool
		val     time.Time
		wantSet bool
	}{
		{
			name:    "Success: unset value round-trips as unset",
			set:     false,
			wantSet: false,
		},
		{
			name:    "Success: set value round-trips as set",
			set:     true,
			val:     time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
			wantSet: true,
		},
	}

	for _, test := range tests {
		var a AtomicValue[time.Time]
		if test.set {
			a.Set(test.val)
		}

		b, err := json.Marshal(&a)
		if err != nil {
			t.Errorf("TestAtomicValueJSON(%s): marshal: got err == %s, want err == nil", test.name, err)
			continue
		}

		var got AtomicValue[time.Time]
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("TestAtomicValueJSON(%s): unmarshal: got err == %s, want err == nil", test.name, err)
			continue
		}

		if gotSet := got.IsSet(); gotSet != test.wantSet {
			t.Errorf("TestAtomicValueJSON(%s): value materialized == %v, want %v", test.name, gotSet, test.wantSet)
		}
		if !got.Get().Equal(a.Get()) {
			t.Errorf("TestAtomicValueJSON(%s): got %v, want %v", test.name, got.Get(), a.Get())
		}
	}
}

func TestAtomicValueIsSet(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		// change is applied to a new, unset AtomicValue.
		change   func(a *AtomicValue[time.Time])
		wantSet  bool
		wantJSON string
	}{
		{
			name:     "Success: a new value is unset",
			change:   func(a *AtomicValue[time.Time]) {},
			wantJSON: "null",
		},
		{
			name:     "Success: a value Set to a non-zero value is set",
			change:   func(a *AtomicValue[time.Time]) { a.Set(when) },
			wantSet:  true,
			wantJSON: `"2026-07-07T00:00:00Z"`,
		},
		{
			name:     "Success: a value Set to the zero value is set",
			change:   func(a *AtomicValue[time.Time]) { a.Set(time.Time{}) },
			wantSet:  true,
			wantJSON: `"0001-01-01T00:00:00Z"`,
		},
		{
			name:     "Success: a value Set and then Cleared is unset",
			change:   func(a *AtomicValue[time.Time]) { a.Set(when); a.Clear() },
			wantJSON: "null",
		},
		{
			name:     "Success: Clearing an unset value leaves it unset",
			change:   func(a *AtomicValue[time.Time]) { a.Clear() },
			wantJSON: "null",
		},
	}

	for _, test := range tests {
		var a AtomicValue[time.Time]
		test.change(&a)

		if got := a.IsSet(); got != test.wantSet {
			t.Errorf("TestAtomicValueIsSet(%s): got IsSet() == %v, want %v", test.name, got, test.wantSet)
		}
		b, err := json.Marshal(&a)
		if err != nil {
			t.Errorf("TestAtomicValueIsSet(%s): marshal: got err == %s, want err == nil", test.name, err)
			continue
		}
		if string(b) != test.wantJSON {
			t.Errorf("TestAtomicValueIsSet(%s): got JSON %s, want %s", test.name, b, test.wantJSON)
		}
	}
}

func TestAtomicSliceIsSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// change is applied to a new, unset AtomicSlice.
		change   func(a *AtomicSlice[int])
		wantSet  bool
		wantJSON string
	}{
		{
			name:     "Success: a new slice is unset",
			change:   func(a *AtomicSlice[int]) {},
			wantJSON: "null",
		},
		{
			name:     "Success: a slice Set to a non-empty slice is set",
			change:   func(a *AtomicSlice[int]) { a.Set([]int{1, 2}) },
			wantSet:  true,
			wantJSON: "[1,2]",
		},
		{
			name:     "Success: a slice Set to nil is set",
			change:   func(a *AtomicSlice[int]) { a.Set(nil) },
			wantSet:  true,
			wantJSON: "[]",
		},
		{
			name:     "Success: a slice Appended to is set",
			change:   func(a *AtomicSlice[int]) { a.Append(1) },
			wantSet:  true,
			wantJSON: "[1]",
		},
		{
			name:     "Success: a slice Set and then Cleared is unset",
			change:   func(a *AtomicSlice[int]) { a.Set([]int{1, 2}); a.Clear() },
			wantJSON: "null",
		},
		{
			name:     "Success: Clearing an unset slice leaves it unset",
			change:   func(a *AtomicSlice[int]) { a.Clear() },
			wantJSON: "null",
		},
	}

	for _, test := range tests {
		var a AtomicSlice[int]
		test.change(&a)

		if got := a.IsSet(); got != test.wantSet {
			t.Errorf("TestAtomicSliceIsSet(%s): got IsSet() == %v, want %v", test.name, got, test.wantSet)
		}
		b, err := json.Marshal(&a)
		if err != nil {
			t.Errorf("TestAtomicSliceIsSet(%s): marshal: got err == %s, want err == nil", test.name, err)
			continue
		}
		if string(b) != test.wantJSON {
			t.Errorf("TestAtomicSliceIsSet(%s): got JSON %s, want %s", test.name, b, test.wantJSON)
		}
	}
}

// TestAtomicSliceJSON verifies JSON round-tripping of AtomicSlice. An unset slice
// marshals to null; unmarshaling null must leave the slice unset (nil pointer),
// symmetric with MarshalJSON, so a round-tripped object still matches its origin.
func TestAtomicSliceJSON(t *testing.T) {
	tests := []struct {
		name    string
		set     bool
		val     []int
		wantSet bool
	}{
		{
			name:    "Success: unset slice round-trips as unset",
			set:     false,
			wantSet: false,
		},
		{
			name:    "Success: set slice round-trips as set",
			set:     true,
			val:     []int{1, 2, 3},
			wantSet: true,
		},
	}

	for _, test := range tests {
		var a AtomicSlice[int]
		if test.set {
			a.Set(test.val)
		}

		b, err := json.Marshal(&a)
		if err != nil {
			t.Errorf("TestAtomicSliceJSON(%s): marshal: got err == %s, want err == nil", test.name, err)
			continue
		}

		var got AtomicSlice[int]
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("TestAtomicSliceJSON(%s): unmarshal: got err == %s, want err == nil", test.name, err)
			continue
		}

		if gotSet := got.IsSet(); gotSet != test.wantSet {
			t.Errorf("TestAtomicSliceJSON(%s): value materialized == %v, want %v", test.name, gotSet, test.wantSet)
		}
	}
}
