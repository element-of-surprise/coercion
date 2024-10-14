// Package context provides both the stdlib context package and functions
// for managing the context in the SDK.
// This package is a drop-in replacement for the stdlib context package.
package context

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// planIDKey is a key for the planID in context.Value .
type planIDKey struct{}

// actionIDKey is a key for the actionID in context.Value .
type actionIDKey struct{}

// PlanID returns planID from a Context.
func PlanID(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(planIDKey{}).(uuid.UUID)
	if ok {
		return id
	}

	return uuid.Nil
}

// SetPlanID sets the planID for context.
func SetPlanID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, planIDKey{}, id)
}

// ActionID returns actionID from a Context.
func ActionID(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(actionIDKey{}).(uuid.UUID)
	if ok {
		return id
	}
	return uuid.Nil
}

// SetActionID sets the actionID for context.
func SetActionID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, actionIDKey{}, id)
}

// Everything after this is copied from the stdlib context package.

func AfterFunc(ctx Context, f func()) (stop func() bool) {
	return context.AfterFunc(ctx, f)
}
func Cause(c Context) error {
	return context.Cause(c)
}
func WithCancel(parent Context) (ctx Context, cancel CancelFunc) {
	return context.WithCancel(parent)
}
func WithCancelCause(parent Context) (ctx Context, cancel CancelCauseFunc) {
	return context.WithCancelCause(parent)
}
func WithDeadline(parent Context, d time.Time) (Context, CancelFunc) {
	return context.WithDeadline(parent, d)
}
func WithDeadlineCause(parent Context, d time.Time, cause error) (Context, CancelFunc) {
	return context.WithDeadlineCause(parent, d, cause)
}
func WithTimeout(parent Context, timeout time.Duration) (Context, CancelFunc) {
	return context.WithTimeout(parent, timeout)
}
func WithTimeoutCause(parent Context, timeout time.Duration, cause error) (Context, CancelFunc) {
	return context.WithTimeoutCause(parent, timeout, cause)
}

type CancelCauseFunc = context.CancelCauseFunc
type CancelFunc = context.CancelFunc
type Context = context.Context

func Background() Context {
	return context.Background()
}
func TODO() Context {
	return context.TODO()
}
func WithValue(parent Context, key, val any) Context {
	return context.WithValue(parent, key, val)
}
func WithoutCancel(parent Context) Context {
	return context.WithoutCancel(parent)
}
