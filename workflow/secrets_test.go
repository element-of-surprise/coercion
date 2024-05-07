package workflow

import (
	"reflect"
	"testing"

	"github.com/kylelemons/godebug/pretty"
)

// Example structs for testing
type User struct {
	Username string
	Password string `coerce:"secure"`
}

type User2 struct {
	Username string
	Password string `coerce:"ignore"`
}

type Config struct {
	APIKey   string `coerce:"secure"`
	Endpoint string
}

type NestedConfig struct {
	Detail struct {
		SigningKey string `coerce:"secure"`
	}
}

type NestedConfig2 struct {
	Detail struct {
		SigningKey string `coerce:"secure"`
		Nested     NestedConfig
	}
}

type NoSecrets struct {
	Detail struct {
		NothingHere string
	}
}

func TestSecure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  any
	}{
		{
			name:  "No secrets",
			value: &NoSecrets{},
			want:  &NoSecrets{},
		},
		{
			name:  "Password should be reset to empty string",
			value: &User{Password: "password"},
			want:  &User{Password: ""},
		},
		{
			name:  "Password should be left alone",
			value: &User2{Password: "pass"},
			want:  &User2{Password: "pass"},
		},
		{
			name: "Fields should be secured in multiple levels",
			value: &NestedConfig2{
				Detail: struct {
					SigningKey string `coerce:"secure"`
					Nested     NestedConfig
				}{
					SigningKey: "signing",
					Nested: NestedConfig{
						Detail: struct {
							SigningKey string `coerce:"secure"`
						}{
							SigningKey: "nested",
						},
					},
				},
			},
			want: &NestedConfig2{
				Detail: struct {
					SigningKey string `coerce:"secure"`
					Nested     NestedConfig
				}{
					SigningKey: "",
					Nested: NestedConfig{
						Detail: struct {
							SigningKey string `coerce:"secure"`
						}{
							SigningKey: "",
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		secure(test.value)
		if diff := pretty.Compare(test.value, test.want); diff != "" {
			t.Errorf("TestSecure(%s): -got/+want:\n%v", test.name, diff)
		}
	}
}

type tagsStruct struct {
	FieldA string `coerce:"secure"`
	FieldB string `coerce:"secure,ignored"`
	FieldC string `coerce:" "`
}

func TestGetTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    reflect.StructField
		want tags
	}{
		{
			name: "Success: FieldA",
			f:    reflect.TypeOf(tagsStruct{}).Field(0),
			want: tags{"secure": true},
		},
		{
			name: "Success: FieldB",
			f:    reflect.TypeOf(tagsStruct{}).Field(1),
			want: tags{"secure": true, "ignored": true},
		},
		{
			name: "Success: FieldC",
			f:    reflect.TypeOf(tagsStruct{}).Field(2),
			want: nil,
		},
	}

	for _, test := range tests {
		got := getTags(test.f)
		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestGetTags(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
