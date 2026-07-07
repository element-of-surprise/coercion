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

		if gotSet := got.value.Load() != nil; gotSet != test.wantSet {
			t.Errorf("TestAtomicValueJSON(%s): value materialized == %v, want %v", test.name, gotSet, test.wantSet)
		}
		if !got.Get().Equal(a.Get()) {
			t.Errorf("TestAtomicValueJSON(%s): got %v, want %v", test.name, got.Get(), a.Get())
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

		if gotSet := got.value.Load() != nil; gotSet != test.wantSet {
			t.Errorf("TestAtomicSliceJSON(%s): value materialized == %v, want %v", test.name, gotSet, test.wantSet)
		}
	}
}
