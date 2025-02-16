// Package types exists so to prevent circular dependencies between transactions/ and cosmosdb/ packages.
package types

//go:generate stringer -type=Type

// Type is the type of cosmosdb document.
type Type uint8

const (
	// Unknown indicates that a type was not provided. This is a bug.
	Unknown Type = 0
	// Plan indicates that the document stores a plan.
	Plan Type = 1
	// Block indicates that the document stores a block.
	Block Type = 2
	// Checks indicates that the document stores checks.
	Checks Type = 3
	// Sequence indicates that the document stores a sequence.
	Sequence Type = 4
	// Action indicates that the document stores an action.
	Action Type = 5
)
