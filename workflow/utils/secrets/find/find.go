// Package find provides functions to find secrets in types. This is separate from
// secure/ to avoid circular dependencies where it will be used.
package find

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// secretRE matches field names that likely contain secrets.
var secretRE = regexp.MustCompile(`(?i)(auth|authorization|bearer|code|credential|cred|cert|hash|jwt|key|password|pass|secret|secure|signing|token)`)

const explain = `
field %q appears to contain sensitive data but is not tagged with 'coerce:"secure"' or 'coerce:"ignore"'.
Add the appropriate tag to indicate how this field should be handled:
  - 'coerce:"secure"' - field will be zeroed when scrubbing secrets
  - 'coerce:"ignore"' - field will be left as-is (use when the name is misleading)
`

// InsecureSecrets walks the value and returns an error if any field appears to contain
// sensitive data but is not properly tagged with `coerce:"secure"` or `coerce:"ignore"`.
// This is useful for validating that request/response types properly mark their sensitive fields.
func InsecureSecrets(v any) error {
	return findSecrets(reflect.ValueOf(v), "")
}

// findSecrets recursively walks a value looking for untagged secret fields.
func findSecrets(val reflect.Value, path string) error {
	if !val.IsValid() {
		return nil
	}

	// Dereference pointers
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	// Unwrap interfaces
	if val.Kind() == reflect.Interface {
		if val.IsNil() {
			return nil
		}
		return findSecrets(val.Elem(), path)
	}

	switch val.Kind() {
	case reflect.Struct:
		return findSecretsInStruct(val, path)
	case reflect.Slice, reflect.Array:
		return findSecretsInSlice(val, path)
	case reflect.Map:
		return findSecretsInMap(val, path)
	default:
		return nil
	}
}

// findSecretsInStruct checks each field in a struct for untagged secrets.
func findSecretsInStruct(val reflect.Value, path string) error {
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// Skip unexported fields
		if !fieldType.IsExported() {
			continue
		}

		fieldPath := fieldType.Name
		if path != "" {
			fieldPath = fmt.Sprintf("%s.%s", path, fieldType.Name)
		}

		// Check if this field name looks like a secret
		if secretRE.MatchString(fieldType.Name) {
			tags := getTags(fieldType)
			if !tags.hasTag("secure") && !tags.hasTag("ignore") {
				return fmt.Errorf(strings.TrimSpace(explain), fieldPath)
			}
		}

		// Recurse into nested structures
		if err := findSecrets(field, fieldPath); err != nil {
			return err
		}
	}
	return nil
}

// findSecretsInSlice checks each element in a slice for untagged secrets.
func findSecretsInSlice(val reflect.Value, path string) error {
	for i := 0; i < val.Len(); i++ {
		if err := findSecrets(val.Index(i), path); err != nil {
			return err
		}
	}
	return nil
}

// findSecretsInMap checks each value in a map for untagged secrets.
func findSecretsInMap(val reflect.Value, path string) error {
	iter := val.MapRange()
	for iter.Next() {
		if err := findSecrets(iter.Value(), path); err != nil {
			return err
		}
	}
	return nil
}

// tags is a set of tags for a field.
type tags map[string]bool

func (t tags) hasTag(tag string) bool {
	if t == nil {
		return false
	}
	return t[tag]
}

// getTags returns the coerce tags for a field.
func getTags(f reflect.StructField) tags {
	strTags := f.Tag.Get("coerce")
	if strings.TrimSpace(strTags) == "" {
		return nil
	}
	t := make(tags)
	for tag := range strings.SplitSeq(strTags, ",") {
		tag = strings.TrimSpace(strings.ToLower(tag))
		t[tag] = true
	}
	return t
}
