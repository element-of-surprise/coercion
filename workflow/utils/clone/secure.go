package clone

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/element-of-surprise/coercion/workflow/errors"
)

// SecureStr is the string that is used to replace sensitive information.
// Use this in any tests to compare against the output of Secure. This string
// can be changed and using this constant will ensure that all tests are updated.
const SecureStr = "[secret hidden]"

// Secure removes sensitive information from a struct that is marked with the `coerce:"secure"` tag.
// It does not handle arrays and if you have some really bizare struct you might find it skipped
// something. Like a *map[string]*any that stores an any storing an *any that stores a *slice of struct. It probably
// will work, but I certainly haven't tested every iteration of weird stuff like that. We also don't handle anything
// not JSON serializable. So private fields are not handled.
func Secure(v any) error {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Ptr {
		return errors.E(context.Background(), errors.CatInternal, errors.TypeParameter, errors.New("value must be a pointer to a struct"))
	}

	if val.IsNil() {
		return nil
	}

	if val.Elem().Kind() != reflect.Struct {
		return errors.E(context.Background(), errors.CatInternal, errors.TypeParameter, errors.New("value must be a pointer to a struct"))
	}
	secureStruct(val)
	return nil
}

// secureStruct recurses through a pointer or reference type in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func securePtrOrRef(val reflect.Value) reflect.Value {
	if val.IsNil() || val.IsZero() {
		return val
	}

	switch val.Kind() {
	case reflect.Ptr:
		return securePtr(val)
	case reflect.Slice:
		return secureSlice(val)
	case reflect.Map:
		return secureMap(val)
	case reflect.Interface:
		return secureInterface(val)
	}

	return val
}

// securePtr recurses throught a pointer to a value in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func securePtr(val reflect.Value) reflect.Value {
	switch val.Elem().Kind() {
	case reflect.Struct:
		return secureStruct(val)
	case reflect.Slice:
		return secureSlice(val)
	case reflect.Map:
		return secureMap(val)
	case reflect.Interface:
		return secureInterface(val)
	}
	return val
}

// secureStruct removes sensitive information from a *struct that is marked with the `coerce:"secure"` tag.
func secureStruct(ptr reflect.Value) reflect.Value {
	if ptr.Kind() != reflect.Ptr || ptr.Elem().Kind() != reflect.Struct {
		panic("value must be a pointer to a struct")
	}
	if ptr.IsNil() {
		return ptr
	}

	val := ptr.Elem()

	// Don't mess with time.Time.
	if _, ok := val.Interface().(time.Time); ok {
		return ptr
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		if !val.Type().Field(i).IsExported() {
			continue
		}
		field := val.Field(i)

		tags := getTags(typ.Field(i))
		if tags.hasTag("secure") {
			if field.Type().Kind() == reflect.String {
				field.SetString("[secret hidden]")
			} else {
				if !field.CanSet() {
					if field.CanAddr() {
						field = field.Addr()
					} else {
						// Diagnostic panic
						panic(fmt.Sprintf("cannot set field(%s) of type %q", typ.Field(i).Name, typ.Field(i).Type.String()))
					}
				}
				field.Set(reflect.Zero(field.Type()))
			}
			continue
		}

		// Okay, there is not secure tag, so we didn't make it a zero value.
		// However, if it is a *struct, struct or interface (containing a *struct or struct),
		// we need to recurse.

		switch field.Kind() {
		case reflect.Struct:
			if _, ok := field.Interface().(time.Time); ok {
				continue
			}
			if field.CanAddr() {
				secureStruct(field.Addr())
			} else {
				fieldPtr := noAddrStruct(field)
				fieldPtr = secureStruct(fieldPtr)
				val.Field(i).Set(fieldPtr.Elem())
			}
		case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map:
			field = securePtrOrRef(field)
			val.Field(i).Set(field)
		}
	}
	return ptr
}

// secureSlice recurses through a slice in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func secureSlice(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Slice {
		panic("val must be a slice")
	}

	if val.IsNil() {
		return val
	}

	for i := 0; i < val.Len(); i++ {
		switch val.Index(i).Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			securePtrOrRef(val.Index(i))
		case reflect.Struct:
			if _, ok := val.Interface().(time.Time); ok {
				continue
			}
			ptr := noAddrStruct(val.Index(i))
			secureStruct(ptr)
			val.Index(i).Set(ptr.Elem())
		}
	}
	return val
}

// secureMap recurses through a map in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields. It does not look at the keys,
// as those aren't valid JSON values if not a string.
func secureMap(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Map {
		panic("val must be a map")
	}

	if val.IsNil() {
		return val
	}

	for _, key := range val.MapKeys() {
		elem := val.MapIndex(key)
		switch elem.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			elem = securePtrOrRef(elem)
			val.SetMapIndex(key, elem)
		case reflect.Struct:
			if _, ok := val.Interface().(time.Time); ok {
				continue
			}
			ptr := noAddrStruct(elem)
			secureStruct(ptr)
			val.SetMapIndex(key, ptr.Elem())
		}
	}
	return val
}

// secureInterface recurses through an interface in order to replace sensitive information
// marked with the `coerce:"secure"` tag on struct fields.
func secureInterface(val reflect.Value) reflect.Value {
	if val.Kind() != reflect.Interface {
		panic("val must be an interface")
	}
	if val.IsNil() {
		return val
	}
	elem := val.Elem()
	switch elem.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice:
		elem = securePtrOrRef(elem)
		val.Set(elem)
	case reflect.Struct:
		if _, ok := val.Interface().(time.Time); ok {
			return val
		}
		ptr := noAddrStruct(elem)
		ptr = secureStruct(ptr)
		val.Set(ptr.Elem())
	}
	return val
}

// noAddrStruct takes a struct and returns a *struct with the same values. This is
// useful when a struct cannot have Set() or Addr() called on it.
func noAddrStruct(orig reflect.Value) reflect.Value {
	if orig.Kind() != reflect.Struct {
		panic("orig must be a struct, not " + orig.Kind().String())
	}

	// Create a new instance of the struct type of original
	// Note: reflect.New returns a pointer, so we use Elem to get the actual struct
	ptr := reflect.New(orig.Type())
	n := ptr.Elem()

	// Copy each field from the original to the new instance
	for i := 0; i < orig.NumField(); i++ {
		if !orig.Type().Field(i).IsExported() {
			continue
		}

		nf := n.Field(i)
		of := orig.Field(i)

		// Ensure that the field is settable
		if !nf.CanSet() {
			panic("bug: field is not settable")
		}
		nf.Set(of)
	}
	return ptr
}
