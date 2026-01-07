// Secure is a package that provides utilities for securing sensitive information in workflow plans.
package secure

import (
	"reflect"
	"strings"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// Plan sets all fields in anywhere in the Plan that are tagged with `coerce:"secure"` to their zero value.
// This is only useful when logging or displaying Plans to users, to avoid leaking sensitive information. It
// is not safe to run on a Plan you intend to execute, as it will remove any secure information needed for execution.
func Plan(p *workflow.Plan) {
	for item := range walk.Plan(p) {
		switch item.Value.Type() {
		case workflow.OTAction:
			action(item.Action())
		case workflow.OTCheck:
			c := item.Checks()
			for _, a := range c.Actions {
				action(a)
			}
		default:
			continue
		}
	}
}

// action sets all fields in the Action that are tagged with `coerce:"secure"` to their zero value.
func action(a *workflow.Action) {
	n := make([]workflow.Attempt, len(a.Attempts.Get()))
	a.Req = item(a.Req)
	for i, attempt := range a.Attempts.Get() {
		n[i] = item(attempt).(workflow.Attempt)
	}
	a.Attempts.Set(n)
}

// item sets all fields in v that are tagged with `coerce:"secure"` to their zero value.
// This is only useful when logging or displaying items to users, to avoid leaking sensitive information.
// It is not safe to run on an item you intend to use, as it will remove any secure information needed.
// If v is a pointer, it is modified in place. If v is a value, a modified copy is returned.
func item(v any) any {
	val := reflect.ValueOf(v)
	isPtr := val.Kind() == reflect.Ptr
	if isPtr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return v
	}

	// If not addressable (passed by value), create an addressable copy.
	if !val.CanAddr() {
		ptr := reflect.New(val.Type())
		ptr.Elem().Set(val)
		val = ptr.Elem()
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if !field.CanSet() {
			continue
		}

		tags := getTags(typ.Field(i))
		if tags.hasTag("secure") {
			field.Set(reflect.Zero(field.Type()))
			continue
		}

		// Recursively coerce nested structs
		if field.Kind() == reflect.Struct {
			field.Set(reflect.ValueOf(item(field.Addr().Interface())))
		} else if field.Kind() == reflect.Ptr && !field.IsNil() && field.Elem().Kind() == reflect.Struct {
			item(field.Interface())
		}
	}
	return val.Interface()
}

// tags is a set of tags for a field.
type tags map[string]bool

func (t tags) hasTag(tag string) bool {
	if t == nil {
		return false
	}
	return t[tag]
}

// getTags returns the tags for a field.
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
