// Package secure provides utilities for scrubbing sensitive information from workflow plans.
package secure

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// fieldHandler is called for each struct field during walking.
// It receives the field metadata and the current path.
// Returns:
//   - zero: whether to set this field to its zero value
//   - err: any error that should stop walking
type fieldHandler func(field reflect.StructField, path string) (zero bool, err error)

// Plan sets all fields anywhere in the Plan that are tagged with `coerce:"secure"` to their zero value.
// This is only useful when logging or displaying Plans to users, to avoid leaking sensitive information. It
// is not safe to run on a Plan you intend to execute, as it will remove any secure information needed for execution.
func Plan(p *workflow.Plan) {
	for item := range walk.Plan(p) {
		switch item.Value.Type() {
		case workflow.OTAction:
			scrubAction(item.Action())
		case workflow.OTCheck:
			c := item.Checks()
			for _, a := range c.Actions {
				scrubAction(a)
			}
		default:
			continue
		}
	}
}

// scrubAction sets all fields in the Action that are tagged with `coerce:"secure"` to their zero value.
func scrubAction(a *workflow.Action) {
	n := make([]workflow.Attempt, len(a.Attempts.Get()))
	a.Req, _ = walkValue(a.Req, "", scrubHandler)
	for i, attempt := range a.Attempts.Get() {
		result, _ := walkValue(attempt, "", scrubHandler)
		n[i] = result.(workflow.Attempt)
	}
	a.Attempts.Set(n)
}

// scrubHandler zeros out fields tagged with "secure".
func scrubHandler(field reflect.StructField, path string) (zero bool, err error) {
	tags := getTags(field)
	if tags.hasTag("secure") {
		return true, nil
	}
	return false, nil
}

// walkValue traverses a value and its nested structures, calling handler for each struct field.
// If handler returns zero=true, the field is set to its zero value.
// If handler returns an error, walking stops and the error is returned.
// Returns the (potentially modified) value.
func walkValue(v any, path string, handler fieldHandler) (any, error) {
	val := reflect.ValueOf(v)
	if !val.IsValid() {
		return v, nil
	}

	isPtr := val.Kind() == reflect.Pointer
	if isPtr {
		if val.IsNil() {
			return v, nil
		}
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.Struct:
		return walkStruct(val, path, handler)
	case reflect.Slice:
		return walkSlice(val, isPtr, path, handler)
	case reflect.Map:
		return walkMap(val, isPtr, path, handler)
	case reflect.Interface:
		if val.IsNil() {
			return v, nil
		}
		return walkValue(val.Elem().Interface(), path, handler)
	default:
		return v, nil
	}
}

// walkStruct processes a struct value, calling handler for each field.
func walkStruct(val reflect.Value, path string, handler fieldHandler) (any, error) {
	// If not addressable (passed by value), create an addressable copy.
	if !val.CanAddr() {
		ptr := reflect.New(val.Type())
		ptr.Elem().Set(val)
		val = ptr.Elem()
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		if !field.CanSet() {
			continue
		}

		fieldPath := fieldType.Name
		if path != "" {
			fieldPath = fmt.Sprintf("%s.%s", path, fieldType.Name)
		}

		zero, err := handler(fieldType, fieldPath)
		if err != nil {
			return nil, err
		}
		if zero {
			field.Set(reflect.Zero(field.Type()))
			continue
		}

		// Recursively walk nested structs, slices, maps, and interfaces
		switch field.Kind() {
		case reflect.Struct:
			result, err := walkValue(field.Addr().Interface(), fieldPath, handler)
			if err != nil {
				return nil, err
			}
			field.Set(reflect.ValueOf(result))
		case reflect.Pointer:
			if !field.IsNil() && field.Elem().Kind() == reflect.Struct {
				if _, err := walkValue(field.Interface(), fieldPath, handler); err != nil {
					return nil, err
				}
			}
		case reflect.Slice:
			if field.Len() > 0 {
				result, err := walkValue(field.Interface(), fieldPath, handler)
				if err != nil {
					return nil, err
				}
				field.Set(reflect.ValueOf(result))
			}
		case reflect.Map:
			if field.Len() > 0 {
				result, err := walkValue(field.Interface(), fieldPath, handler)
				if err != nil {
					return nil, err
				}
				field.Set(reflect.ValueOf(result))
			}
		case reflect.Interface:
			if !field.IsNil() {
				result, err := walkValue(field.Interface(), fieldPath, handler)
				if err != nil {
					return nil, err
				}
				if result != nil {
					field.Set(reflect.ValueOf(result))
				}
			}
		}
	}
	return val.Interface(), nil
}

// walkSlice processes a slice, recursively processing struct elements.
func walkSlice(val reflect.Value, wasPtr bool, path string, handler fieldHandler) (any, error) {
	if val.Len() == 0 {
		if wasPtr {
			return val.Addr().Interface(), nil
		}
		return val.Interface(), nil
	}

	elemType := val.Type().Elem()
	elemIsPtr := elemType.Kind() == reflect.Pointer
	if elemIsPtr {
		elemType = elemType.Elem()
	}

	// Only process if elements are structs or pointers to structs
	if elemType.Kind() != reflect.Struct {
		if wasPtr {
			return val.Addr().Interface(), nil
		}
		return val.Interface(), nil
	}

	// Create a new slice to hold processed elements
	newSlice := reflect.MakeSlice(val.Type(), val.Len(), val.Len())
	for i := 0; i < val.Len(); i++ {
		elem := val.Index(i)
		processed, err := walkValue(elem.Interface(), path, handler)
		if err != nil {
			return nil, err
		}
		processedVal := reflect.ValueOf(processed)

		// If the original element was a pointer, we need to wrap the result in a pointer
		if elemIsPtr {
			ptr := reflect.New(elemType)
			ptr.Elem().Set(processedVal)
			newSlice.Index(i).Set(ptr)
		} else {
			newSlice.Index(i).Set(processedVal)
		}
	}

	if wasPtr {
		ptr := reflect.New(newSlice.Type())
		ptr.Elem().Set(newSlice)
		return ptr.Interface(), nil
	}
	return newSlice.Interface(), nil
}

// walkMap processes a map, recursively processing struct values.
func walkMap(val reflect.Value, wasPtr bool, path string, handler fieldHandler) (any, error) {
	if val.Len() == 0 {
		if wasPtr {
			return val.Addr().Interface(), nil
		}
		return val.Interface(), nil
	}

	valueType := val.Type().Elem()
	valueIsPtr := valueType.Kind() == reflect.Pointer
	if valueIsPtr {
		valueType = valueType.Elem()
	}

	// Only process if values are structs or pointers to structs
	if valueType.Kind() != reflect.Struct {
		if wasPtr {
			return val.Addr().Interface(), nil
		}
		return val.Interface(), nil
	}

	// Create a new map to hold processed values
	newMap := reflect.MakeMap(val.Type())
	iter := val.MapRange()
	for iter.Next() {
		key := iter.Key()
		mapVal := iter.Value()
		processed, err := walkValue(mapVal.Interface(), path, handler)
		if err != nil {
			return nil, err
		}
		processedVal := reflect.ValueOf(processed)

		// If the original value was a pointer, we need to wrap the result in a pointer
		if valueIsPtr {
			ptr := reflect.New(valueType)
			ptr.Elem().Set(processedVal)
			newMap.SetMapIndex(key, ptr)
		} else {
			newMap.SetMapIndex(key, processedVal)
		}
	}

	if wasPtr {
		ptr := reflect.New(newMap.Type())
		ptr.Elem().Set(newMap)
		return ptr.Interface(), nil
	}
	return newMap.Interface(), nil
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
