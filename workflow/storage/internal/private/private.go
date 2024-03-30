// Package private is used to define interfaces that are only implemented by packages in the same module.
package private

// Storage is the interface that must be implemented by all storage packages.
// Because this is public and in an internal package, this can only be implemented
// by packages in the same module.
type Storage interface {
	private()
}
