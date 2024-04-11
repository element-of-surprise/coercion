// Package private is used to define interfaces that are only implemented by packages in the same module.
// Because this is defined in an internal package, it can only be implemented by packages in the same module.
// Normally doing this with a standard package local private() means only the package can implement it. This
// extends the reach to the module level.
package private

// Storage is the interface that must be implemented by all storage packages.
// Because this is public and in an internal package, this can only be implemented
// by packages in the same module.
type Storage interface {
	private()
}
