package workflow

import (
	"slices"
	"sync/atomic"

	"github.com/go-json-experiment/json"
)

// AtomicValue is a generic atomic value that can be used to store types that
// are safe to be copied by value, like a non-pointer struct. Don't use for
// scalars like int, float, or string; use atomic.Int64, atomic.Float64, or atomic.Value instead.
// It supports JSON marshaling and unmarshaling as must the underlying type T.
type AtomicValue[T any] struct {
	value atomic.Pointer[T]
	// MarshalJSONer is a function that marshals the value to JSON.
	// If not set, the default JSON marshaler is used.
	MarshalJSONer func() ([]byte, error)
	// UnmarshalJSONer is a function that unmarshals the value from JSON.
	// If not set, the default JSON unmarshaler is used.
	UnmarshalJSONer func([]byte) error
}

// Get returns a copy of the stored value. This is safe for concurrent access
// as modifications to the returned value won't affect the stored value.
// If the stored value is nil, returns the zero value of T.
func (a *AtomicValue[T]) Get() T {
	if v := a.value.Load(); v != nil {
		return *v
	}
	var zero T
	return zero
}

// Set stores a copy of the provided value. This is safe for concurrent access
// as the caller's value is copied before storing.
func (a *AtomicValue[T]) Set(val T) {
	a.value.Store(&val)
}

// MarshalJSON marshals the value to JSON.
func (a *AtomicValue[T]) MarshalJSON() ([]byte, error) {
	if a.MarshalJSONer != nil {
		return a.MarshalJSONer()
	}
	v := a.value.Load()
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// UnmarshalJSON unmarshals the value from JSON.
func (a *AtomicValue[T]) UnmarshalJSON(data []byte) error {
	if a.UnmarshalJSONer != nil {
		return a.UnmarshalJSONer(data)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	a.value.Store(&v)
	return nil
}

// AtomicSlice is a generic atomic slice that can be used to store slices of any type.
// This returns a copy of the slice on Get, so modifications to the returned slice won't affect
// the stored slice. Don't use for slices of pointers if you want to avoid data races.
// It supports JSON marshaling and unmarshaling as must the underlying type T.
type AtomicSlice[T any] struct {
	value atomic.Pointer[[]T]
	// MarshalJSONer is a function that marshals the value to JSON.
	// If not set, the default JSON marshaler is used.
	MarshalJSONer func() ([]byte, error)
	// UnmarshalJSONer is a function that unmarshals the value from JSON.
	// If not set, the default JSON unmarshaler is used.
	UnmarshalJSONer func([]byte) error
}

// Get returns a copy of the stored value. This is safe for concurrent access
// as modifications to the returned value won't affect the stored value.
// If the stored value is nil, returns the zero value of T.
func (a *AtomicSlice[T]) Get() []T {
	if v := a.value.Load(); v != nil {
		r := slices.Clone(*v)
		return r
	}
	return nil
}

// Set stores a copy of the provided value. This is safe for concurrent access
// as the caller's value is copied before storing.
func (a *AtomicSlice[T]) Set(val []T) {
	a.value.Store(&val)
}

// Append appends items to the slice atomically using compare-and-swap.
func (a *AtomicSlice[T]) Append(items ...T) {
	for {
		old := a.value.Load()
		var newSlice []T
		if old != nil {
			newSlice = make([]T, len(*old), len(*old)+len(items))
			copy(newSlice, *old)
		} else {
			newSlice = make([]T, 0, len(items))
		}
		newSlice = append(newSlice, items...)
		if a.value.CompareAndSwap(old, &newSlice) {
			return
		}
	}
}

// MarshalJSON marshals the value to JSON.
func (a *AtomicSlice[T]) MarshalJSON() ([]byte, error) {
	if a.MarshalJSONer != nil {
		return a.MarshalJSONer()
	}
	v := a.value.Load()
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// UnmarshalJSON unmarshals the value from JSON.
func (a *AtomicSlice[T]) UnmarshalJSON(data []byte) error {
	if a.UnmarshalJSONer != nil {
		return a.UnmarshalJSONer(data)
	}
	var v = []T{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	a.value.Store(&v)
	return nil
}
