package clone

import (
	"reflect"
	"testing"

	"github.com/element-of-surprise/coercion/workflow"

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

type AnyHolder struct {
	Holding any
}

type AnyHolderSecure struct {
	Holding any `coerce:"secure"`
}

type NestedConfig3 struct {
	Detail any
}

func TestSecure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  any
	}{
		{
			name:  "Success: a struct with no secrets is unchanged",
			value: &NoSecrets{},
			want:  &NoSecrets{},
		},
		{
			name:  "Success: a password marked secure is reset to an empty string",
			value: &User{Password: "password"},
			want:  &User{Password: "[secret hidden]"},
		},
		{
			name:  "Success: a password not marked secure is left alone",
			value: &User2{Password: "pass"},
			want:  &User2{Password: "pass"},
		},
		{
			name: "Success: secure fields are secured at every level of nesting",
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
					SigningKey: "[secret hidden]",
					Nested: NestedConfig{
						Detail: struct {
							SigningKey string `coerce:"secure"`
						}{
							SigningKey: "[secret hidden]",
						},
					},
				},
			},
		},
		{
			name: "Success: a struct stored in an any is secured",
			value: &AnyHolder{
				Holding: &NestedConfig3{
					Detail: Config{
						APIKey:   "key",
						Endpoint: "endpoint",
					},
				},
			},
			want: &AnyHolder{
				Holding: &NestedConfig3{
					Detail: Config{
						APIKey:   "[secret hidden]",
						Endpoint: "endpoint",
					},
				},
			},
		},
		{
			name: "Success: an entire struct stored inside an any is secured",
			value: &AnyHolderSecure{
				Holding: NestedConfig3{Detail: "blah"},
			},
			want: &AnyHolderSecure{
				Holding: nil,
			},
		},
		{
			name: "Success: secrets in a DeferBatch's embedded Sequence actions are wiped",
			value: &workflow.DeferBatch{
				FailElement: true,
				Sequence: workflow.Sequence{
					Name:  "cleanup",
					Descr: "cleanup",
					Actions: []*workflow.Action{
						{Name: "action1", Req: Req{Data: "hello"}},
					},
				},
			},
			want: &workflow.DeferBatch{
				FailElement: true,
				Sequence: workflow.Sequence{
					Name:  "cleanup",
					Descr: "cleanup",
					Actions: []*workflow.Action{
						{Name: "action1", Req: Req{Data: SecureStr}},
					},
				},
			},
		},
		{
			name: "Success: secrets in the nested batches of DeferredActions are wiped",
			value: &workflow.DeferredActions{
				DeferredBatches: []*workflow.DeferBatch{
					{
						When: workflow.OnFailure,
						Sequence: workflow.Sequence{
							Name:  "fail",
							Descr: "fail",
							Actions: []*workflow.Action{
								{Name: "fa", Req: Req{Data: "fail_secret"}},
							},
						},
					},
					{
						When: workflow.OnSuccess,
						Sequence: workflow.Sequence{
							Name:  "success",
							Descr: "success",
							Actions: []*workflow.Action{
								{Name: "sa", Req: Req{Data: "success_secret"}},
							},
						},
					},
				},
			},
			want: &workflow.DeferredActions{
				DeferredBatches: []*workflow.DeferBatch{
					{
						When: workflow.OnFailure,
						Sequence: workflow.Sequence{
							Name:  "fail",
							Descr: "fail",
							Actions: []*workflow.Action{
								{Name: "fa", Req: Req{Data: SecureStr}},
							},
						},
					},
					{
						When: workflow.OnSuccess,
						Sequence: workflow.Sequence{
							Name:  "success",
							Descr: "success",
							Actions: []*workflow.Action{
								{Name: "sa", Req: Req{Data: SecureStr}},
							},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		Secure(test.value)
		if diff := pretty.Compare(test.want, test.value); diff != "" {
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
