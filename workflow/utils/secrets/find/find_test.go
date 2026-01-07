package find

import (
	"reflect"
	"testing"

	"github.com/kylelemons/godebug/pretty"
)

// Test structs
type User struct {
	Username string
	Password string `coerce:"secure"`
}

type UntaggedPassword struct {
	Username string
	Password string // Missing coerce tag - should trigger error
}

type TaggedPassword struct {
	Username string
	Password string `coerce:"secure"`
}

type IgnoredKey struct {
	Username string
	APIKey   string `coerce:"ignore"` // Explicitly ignored
}

type NoSecrets struct {
	Detail struct {
		NothingHere string
	}
}

type NestedUntagged struct {
	Config struct {
		SecretToken string // Untagged secret in nested struct
	}
}

type NestedTagged struct {
	Config struct {
		SecretToken string `coerce:"secure"`
	}
}

type SliceUntagged struct {
	Users []UntaggedPassword
}

type MapUntagged struct {
	Configs map[string]UntaggedPassword
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

func TestSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   any
		wantErr bool
	}{
		{
			name:    "Success: non-struct value returns no error",
			input:   "hello",
			wantErr: false,
		},
		{
			name:    "Success: struct with no secret-like fields",
			input:   NoSecrets{},
			wantErr: false,
		},
		{
			name:    "Success: struct with properly tagged secure field",
			input:   TaggedPassword{Username: "john", Password: "secret"},
			wantErr: false,
		},
		{
			name:    "Success: struct with ignored field",
			input:   IgnoredKey{Username: "john", APIKey: "key"},
			wantErr: false,
		},
		{
			name: "Success: nested struct with properly tagged field",
			input: func() NestedTagged {
				nt := NestedTagged{}
				nt.Config.SecretToken = "token"
				return nt
			}(),
			wantErr: false,
		},
		{
			name:    "Error: struct with untagged password field",
			input:   UntaggedPassword{Username: "john", Password: "secret"},
			wantErr: true,
		},
		{
			name: "Error: nested struct with untagged secret field",
			input: func() NestedUntagged {
				nu := NestedUntagged{}
				nu.Config.SecretToken = "token"
				return nu
			}(),
			wantErr: true,
		},
		{
			name: "Error: slice containing struct with untagged secret",
			input: SliceUntagged{
				Users: []UntaggedPassword{
					{Username: "john", Password: "secret"},
				},
			},
			wantErr: true,
		},
		{
			name: "Error: map containing struct with untagged secret",
			input: MapUntagged{
				Configs: map[string]UntaggedPassword{
					"user1": {Username: "john", Password: "secret"},
				},
			},
			wantErr: true,
		},
		{
			name:    "Success: pointer to properly tagged struct",
			input:   &TaggedPassword{Username: "john", Password: "secret"},
			wantErr: false,
		},
		{
			name:    "Error: pointer to struct with untagged secret",
			input:   &UntaggedPassword{Username: "john", Password: "secret"},
			wantErr: true,
		},
		{
			name:    "Success: nil value returns no error",
			input:   nil,
			wantErr: false,
		},
		{
			name:    "Success: empty slice returns no error",
			input:   []UntaggedPassword{},
			wantErr: false,
		},
		{
			name:    "Success: empty map returns no error",
			input:   map[string]UntaggedPassword{},
			wantErr: false,
		},
		{
			name: "Success: slice of properly tagged structs",
			input: []User{
				{Username: "john", Password: "secret"},
			},
			wantErr: false,
		},
		{
			name: "Success: map of properly tagged structs",
			input: map[string]User{
				"user1": {Username: "john", Password: "secret"},
			},
			wantErr: false,
		},
		{
			name: "Error: slice of pointers to untagged structs",
			input: []*UntaggedPassword{
				{Username: "john", Password: "secret"},
			},
			wantErr: true,
		},
		{
			name: "Error: map of pointers to untagged structs",
			input: map[string]*UntaggedPassword{
				"user1": {Username: "john", Password: "secret"},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		err := InsecureSecrets(test.input)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("TestSecrets(%s): got err == nil, want err != nil", test.name)
		case err != nil && !test.wantErr:
			t.Errorf("TestSecrets(%s): got err == %s, want err == nil", test.name, err)
		}
	}
}
