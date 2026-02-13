package secure

import (
	"reflect"
	"testing"
	"time"

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

type WithSlice struct {
	Users []User
}

type WithMap struct {
	Configs map[string]Config
}

type WithPtrSlice struct {
	Users []*User
}

type WithPtrMap struct {
	Configs map[string]*Config
}

type tagsStruct struct {
	FieldA string `coerce:"secure"`
	FieldB string `coerce:"secure,ignored"`
	FieldC string `coerce:" "`
}

type InterfaceWrapper struct {
	Interface
}

type Interface interface {
	MakeNoise() string
}

type Lion struct {}

func (*Lion) MakeNoise() string {
	return "roar"
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

func TestWalkValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{
			name:  "Success: non-struct value returns unchanged",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "Success: int value returns unchanged",
			input: 42,
			want:  42,
		},
		{
			name:  "Success: struct with secure tag has field zeroed",
			input: User{Username: "john", Password: "secret123"},
			want:  User{Username: "john", Password: ""},
		},
		{
			name:  "Success: interface field with pointer",
			input: InterfaceWrapper{Interface: &Lion{}},
			want:  InterfaceWrapper{Interface: &Lion{}},
		},
		{
			name:  "Success: struct without secure tag remains unchanged",
			input: User2{Username: "john", Password: "secret123"},
			want:  User2{Username: "john", Password: "secret123"},
		},
		{
			name:  "Success: config with secure APIKey",
			input: Config{APIKey: "my-api-key", Endpoint: "https://api.example.com"},
			want:  Config{APIKey: "", Endpoint: "https://api.example.com"},
		},
		{
			name: "Success: nested struct with secure field",
			input: func() NestedConfig {
				nc := NestedConfig{}
				nc.Detail.SigningKey = "secret-signing-key"
				return nc
			}(),
			want: func() NestedConfig {
				nc := NestedConfig{}
				nc.Detail.SigningKey = ""
				return nc
			}(),
		},
		{
			name: "Success: deeply nested struct with secure fields",
			input: func() NestedConfig2 {
				nc := NestedConfig2{}
				nc.Detail.SigningKey = "outer-key"
				nc.Detail.Nested.Detail.SigningKey = "inner-key"
				return nc
			}(),
			want: func() NestedConfig2 {
				nc := NestedConfig2{}
				nc.Detail.SigningKey = ""
				nc.Detail.Nested.Detail.SigningKey = ""
				return nc
			}(),
		},
		{
			name: "Success: struct with no secrets remains unchanged",
			input: func() NoSecrets {
				ns := NoSecrets{}
				ns.Detail.NothingHere = "visible"
				return ns
			}(),
			want: func() NoSecrets {
				ns := NoSecrets{}
				ns.Detail.NothingHere = "visible"
				return ns
			}(),
		},
		{
			name:  "Success: pointer to struct with secure tag is modified in place",
			input: &User{Username: "john", Password: "secret123"},
			want:  User{Username: "john", Password: ""},
		},
		{
			name:  "Success: pointer to config with secure APIKey is modified in place",
			input: &Config{APIKey: "my-api-key", Endpoint: "https://api.example.com"},
			want:  Config{APIKey: "", Endpoint: "https://api.example.com"},
		},
		{
			name: "Success: pointer to nested struct with secure field is modified in place",
			input: func() *NestedConfig {
				nc := &NestedConfig{}
				nc.Detail.SigningKey = "secret-signing-key"
				return nc
			}(),
			want: func() NestedConfig {
				nc := NestedConfig{}
				nc.Detail.SigningKey = ""
				return nc
			}(),
		},
		{
			name: "Success: slice of structs with secure fields",
			input: []User{
				{Username: "john", Password: "secret1"},
				{Username: "jane", Password: "secret2"},
			},
			want: []User{
				{Username: "john", Password: ""},
				{Username: "jane", Password: ""},
			},
		},
		{
			name:  "Success: empty slice returns unchanged",
			input: []User{},
			want:  []User{},
		},
		{
			name: "Success: slice of pointers to structs with secure fields",
			input: []*User{
				{Username: "john", Password: "secret1"},
				{Username: "jane", Password: "secret2"},
			},
			want: []*User{
				{Username: "john", Password: ""},
				{Username: "jane", Password: ""},
			},
		},
		{
			name: "Success: map with struct values with secure fields",
			input: map[string]Config{
				"prod": {APIKey: "prod-key", Endpoint: "https://prod.example.com"},
				"dev":  {APIKey: "dev-key", Endpoint: "https://dev.example.com"},
			},
			want: map[string]Config{
				"prod": {APIKey: "", Endpoint: "https://prod.example.com"},
				"dev":  {APIKey: "", Endpoint: "https://dev.example.com"},
			},
		},
		{
			name:  "Success: empty map returns unchanged",
			input: map[string]Config{},
			want:  map[string]Config{},
		},
		{
			name: "Success: map with pointer to struct values with secure fields",
			input: map[string]*Config{
				"prod": {APIKey: "prod-key", Endpoint: "https://prod.example.com"},
				"dev":  {APIKey: "dev-key", Endpoint: "https://dev.example.com"},
			},
			want: map[string]*Config{
				"prod": {APIKey: "", Endpoint: "https://prod.example.com"},
				"dev":  {APIKey: "", Endpoint: "https://dev.example.com"},
			},
		},
		{
			name: "Success: struct containing slice of structs",
			input: WithSlice{
				Users: []User{
					{Username: "john", Password: "secret1"},
					{Username: "jane", Password: "secret2"},
				},
			},
			want: WithSlice{
				Users: []User{
					{Username: "john", Password: ""},
					{Username: "jane", Password: ""},
				},
			},
		},
		{
			name: "Success: struct containing map of structs",
			input: WithMap{
				Configs: map[string]Config{
					"prod": {APIKey: "prod-key", Endpoint: "https://prod.example.com"},
				},
			},
			want: WithMap{
				Configs: map[string]Config{
					"prod": {APIKey: "", Endpoint: "https://prod.example.com"},
				},
			},
		},
		{
			name: "Success: struct containing slice of pointer structs",
			input: WithPtrSlice{
				Users: []*User{
					{Username: "john", Password: "secret1"},
				},
			},
			want: WithPtrSlice{
				Users: []*User{
					{Username: "john", Password: ""},
				},
			},
		},
		{
			name: "Success: struct containing map of pointer structs",
			input: WithPtrMap{
				Configs: map[string]*Config{
					"prod": {APIKey: "prod-key", Endpoint: "https://prod.example.com"},
				},
			},
			want: WithPtrMap{
				Configs: map[string]*Config{
					"prod": {APIKey: "", Endpoint: "https://prod.example.com"},
				},
			},
		},
		{
			name:  "Success: slice of non-structs returns unchanged",
			input: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "Success: map with non-struct values returns unchanged",
			input: map[string]int{"a": 1, "b": 2},
			want:  map[string]int{"a": 1, "b": 2},
		},
		{
			name:  "Success: nil value returns unchanged",
			input: nil,
			want:  nil,
		},
	}

	for _, test := range tests {
		got, err := walkValue(test.input, "", scrubHandler)
		if err != nil {
			t.Errorf("TestWalkValue(%s): unexpected error: %v", test.name, err)
			continue
		}
		if diff := pretty.Compare(test.want, got); diff != "" {
			t.Errorf("TestWalkValue(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}

func TestPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plan    *workflow.Plan
		getReq  func(p *workflow.Plan) any
		wantReq any
	}{
		{
			name: "Success: action in sequence has secure field zeroed",
			plan: &workflow.Plan{
				Name:  "test-plan",
				Descr: "test plan description",
				Blocks: []*workflow.Block{
					{
						Name:  "test-block",
						Descr: "test block description",
						Sequences: []*workflow.Sequence{
							{
								Name:  "test-sequence",
								Descr: "test sequence description",
								Actions: []*workflow.Action{
									{
										Name:    "test-action",
										Descr:   "test action description",
										Plugin:  "test-plugin",
										Timeout: 30 * time.Second,
										Req:     User{Username: "john", Password: "secret123"},
									},
								},
							},
						},
					},
				},
			},
			getReq: func(p *workflow.Plan) any {
				return p.Blocks[0].Sequences[0].Actions[0].Req
			},
			wantReq: User{Username: "john", Password: ""},
		},
		{
			name: "Success: action in precheck has secure field zeroed",
			plan: &workflow.Plan{
				Name:  "test-plan",
				Descr: "test plan description",
				PreChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{
							Name:    "precheck-action",
							Descr:   "precheck action description",
							Plugin:  "test-plugin",
							Timeout: 30 * time.Second,
							Req:     Config{APIKey: "my-secret-key", Endpoint: "https://api.example.com"},
						},
					},
				},
				Blocks: []*workflow.Block{
					{
						Name:  "test-block",
						Descr: "test block description",
						Sequences: []*workflow.Sequence{
							{
								Name:  "test-sequence",
								Descr: "test sequence description",
								Actions: []*workflow.Action{
									{
										Name:    "test-action",
										Descr:   "test action description",
										Plugin:  "test-plugin",
										Timeout: 30 * time.Second,
										Req:     "no secrets here",
									},
								},
							},
						},
					},
				},
			},
			getReq: func(p *workflow.Plan) any {
				return p.PreChecks.Actions[0].Req
			},
			wantReq: Config{APIKey: "", Endpoint: "https://api.example.com"},
		},
		{
			name: "Success: action in block precheck has secure field zeroed",
			plan: &workflow.Plan{
				Name:  "test-plan",
				Descr: "test plan description",
				Blocks: []*workflow.Block{
					{
						Name:  "test-block",
						Descr: "test block description",
						PreChecks: &workflow.Checks{
							Actions: []*workflow.Action{
								{
									Name:    "block-precheck",
									Descr:   "block precheck description",
									Plugin:  "test-plugin",
									Timeout: 30 * time.Second,
									Req:     User{Username: "admin", Password: "topsecret"},
								},
							},
						},
						Sequences: []*workflow.Sequence{
							{
								Name:  "test-sequence",
								Descr: "test sequence description",
								Actions: []*workflow.Action{
									{
										Name:    "test-action",
										Descr:   "test action description",
										Plugin:  "test-plugin",
										Timeout: 30 * time.Second,
										Req:     "no secrets",
									},
								},
							},
						},
					},
				},
			},
			getReq: func(p *workflow.Plan) any {
				return p.Blocks[0].PreChecks.Actions[0].Req
			},
			wantReq: User{Username: "admin", Password: ""},
		},
		{
			name: "Success: pointer req in sequence has secure field zeroed",
			plan: &workflow.Plan{
				Name:  "test-plan",
				Descr: "test plan description",
				Blocks: []*workflow.Block{
					{
						Name:  "test-block",
						Descr: "test block description",
						Sequences: []*workflow.Sequence{
							{
								Name:  "test-sequence",
								Descr: "test sequence description",
								Actions: []*workflow.Action{
									{
										Name:    "test-action",
										Descr:   "test action description",
										Plugin:  "test-plugin",
										Timeout: 30 * time.Second,
										Req:     &User{Username: "john", Password: "secret123"},
									},
								},
							},
						},
					},
				},
			},
			getReq: func(p *workflow.Plan) any {
				return p.Blocks[0].Sequences[0].Actions[0].Req
			},
			wantReq: &User{Username: "john", Password: ""},
		},
		{
			name: "Success: pointer req in precheck has secure field zeroed",
			plan: &workflow.Plan{
				Name:  "test-plan",
				Descr: "test plan description",
				PreChecks: &workflow.Checks{
					Actions: []*workflow.Action{
						{
							Name:    "precheck-action",
							Descr:   "precheck action description",
							Plugin:  "test-plugin",
							Timeout: 30 * time.Second,
							Req:     &Config{APIKey: "my-secret-key", Endpoint: "https://api.example.com"},
						},
					},
				},
				Blocks: []*workflow.Block{
					{
						Name:  "test-block",
						Descr: "test block description",
						Sequences: []*workflow.Sequence{
							{
								Name:  "test-sequence",
								Descr: "test sequence description",
								Actions: []*workflow.Action{
									{
										Name:    "test-action",
										Descr:   "test action description",
										Plugin:  "test-plugin",
										Timeout: 30 * time.Second,
										Req:     "no secrets here",
									},
								},
							},
						},
					},
				},
			},
			getReq: func(p *workflow.Plan) any {
				return p.PreChecks.Actions[0].Req
			},
			wantReq: &Config{APIKey: "", Endpoint: "https://api.example.com"},
		},
	}

	for _, test := range tests {
		Plan(test.plan)

		if diff := pretty.Compare(test.wantReq, test.getReq(test.plan)); diff != "" {
			t.Errorf("TestPlan(%s): -want/+got:\n%s", test.name, diff)
		}
	}
}
