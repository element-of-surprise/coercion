package cosmosdb

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

var marshalResult []byte

func TestMarshalPlansEntry(t *testing.T) {
	pe := plansEntry{
		PartitionKey:   "test-partition",
		Swarm:          "test-swarm",
		Type:           workflow.OTPlan,
		ID:             mustUUID(),
		PlanID:         mustUUID(),
		GroupID:        mustUUID(),
		Name:           "test-plan",
		Descr:          "test description",
		Meta:           []byte(`{"test": "meta"}`),
		BypassChecks:   mustUUID(),
		PreChecks:      mustUUID(),
		PostChecks:     mustUUID(),
		ContChecks:     mustUUID(),
		DeferredChecks: mustUUID(),
		Blocks:         []uuid.UUID{mustUUID(), mustUUID()},
		StateStatus:    workflow.Running,
		StateStart:     time.Now(),
		StateEnd:       time.Now().Add(time.Hour),
		SubmitTime:     time.Now(),
		Reason:         workflow.FRUnknown,
		ETag:           azcore.ETag("test-etag"),
	}

	var err error
	marshalResult, err = json.Marshal(pe)
	if err != nil {
		t.Fatalf("Failed to marshal plansEntry: %v", err)
	}
}

func TestMarshalBlocksEntry(t *testing.T) {
	be := blocksEntry{
		PartitionKey:         "test-partition",
		Swarm:                "test-swarm",
		Type:                 workflow.OTBlock,
		ID:                   mustUUID(),
		Key:                  mustUUID(),
		PlanID:               mustUUID(),
		Name:                 "test-block",
		Descr:                "test block description",
		Pos:                  1,
		EntranceDelayISO8601: 5 * time.Second,
		ExitDelayISO8601:     10 * time.Second,
		BypassChecks:         mustUUID(),
		PreChecks:            mustUUID(),
		PostChecks:           mustUUID(),
		ContChecks:           mustUUID(),
		DeferredChecks:       mustUUID(),
		Sequences:            []uuid.UUID{mustUUID(), mustUUID()},
		Concurrency:          3,
		ToleratedFailures:    1,
		StateStatus:          workflow.Running,
		StateStart:           time.Now(),
		StateEnd:             time.Now().Add(time.Hour),
		ETag:                 azcore.ETag("test-etag"),
	}

	var err error
	marshalResult, err = json.Marshal(be)
	if err != nil {
		t.Fatalf("Failed to marshal blocksEntry: %v", err)
	}
}

func TestMarshalChecksEntry(t *testing.T) {
	ce := checksEntry{
		PartitionKey: "test-partition",
		Swarm:        "test-swarm",
		Type:         workflow.OTCheck,
		ID:           mustUUID(),
		Key:          mustUUID(),
		PlanID:       mustUUID(),
		Actions:      []uuid.UUID{mustUUID(), mustUUID()},
		DelayISO8601: 30 * time.Second,
		StateStatus:  workflow.Running,
		StateStart:   time.Now(),
		StateEnd:     time.Now().Add(time.Hour),
		ETag:         azcore.ETag("test-etag"),
	}

	var err error
	marshalResult, err = json.Marshal(ce)
	if err != nil {
		t.Fatalf("Failed to marshal checksEntry: %v", err)
	}
}

func TestMarshalSequencesEntry(t *testing.T) {
	se := sequencesEntry{
		PartitionKey: "test-partition",
		Swarm:        "test-swarm",
		Type:         workflow.OTSequence,
		ID:           mustUUID(),
		Key:          mustUUID(),
		PlanID:       mustUUID(),
		Name:         "test-sequence",
		Descr:        "test sequence description",
		Pos:          2,
		Actions:      []uuid.UUID{mustUUID(), mustUUID()},
		StateStatus:  workflow.Running,
		StateStart:   time.Now(),
		StateEnd:     time.Now().Add(time.Hour),
		ETag:         azcore.ETag("test-etag"),
	}

	var err error
	marshalResult, err = json.Marshal(se)
	if err != nil {
		t.Fatalf("Failed to marshal sequencesEntry: %v", err)
	}
}

func TestMarshalActionsEntry(t *testing.T) {
	ae := actionsEntry{
		PartitionKey:   "test-partition",
		Swarm:          "test-swarm",
		Type:           workflow.OTAction,
		ID:             mustUUID(),
		Key:            mustUUID(),
		PlanID:         mustUUID(),
		Name:           "test-action",
		Descr:          "test action description",
		Pos:            3,
		Plugin:         "test-plugin",
		TimeoutISO8601: 2 * time.Minute,
		Retries:        3,
		Req:            []byte(`{"action": "request"}`),
		Attempts:       []byte(`[{"attempt": 1}]`),
		StateStatus:    workflow.Running,
		StateStart:     time.Now(),
		StateEnd:       time.Now().Add(time.Hour),
		ETag:           azcore.ETag("test-etag"),
	}

	var err error
	marshalResult, err = json.Marshal(ae)
	if err != nil {
		t.Fatalf("Failed to marshal actionsEntry: %v", err)
	}
}

func TestMarshalSearchEntry(t *testing.T) {
	se := searchEntry{
		PartitionKey: "search-partition",
		Swarm:        "test-swarm",
		Name:         "test-search",
		Descr:        "test search description",
		ID:           mustUUID(),
		GroupID:      mustUUID(),
		SubmitTime:   time.Now(),
		StateStatus:  workflow.Running,
		StateStart:   time.Now(),
		StateEnd:     time.Now().Add(time.Hour),
	}

	var err error
	marshalResult, err = json.Marshal(se)
	if err != nil {
		t.Fatalf("Failed to marshal searchEntry: %v", err)
	}
}

func TestMarshalBasicTypes(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
	}{
		{
			name: "byte_array",
			data: struct {
				Data []byte `json:"data"`
			}{
				Data: []byte("hello world"),
			},
		},
		{
			name: "string",
			data: struct {
				Message string `json:"message"`
			}{
				Message: "test string",
			},
		},
		{
			name: "int_values",
			data: struct {
				Count   int   `json:"count"`
				BigNum  int64 `json:"bigNum"`
				SmallNo int32 `json:"smallNo"`
			}{
				Count:   42,
				BigNum:  9223372036854775807,
				SmallNo: 2147483647,
			},
		},
		{
			name: "bool_and_float",
			data: struct {
				IsActive bool    `json:"isActive"`
				Price    float64 `json:"price"`
				Rate     float32 `json:"rate"`
			}{
				IsActive: true,
				Price:    123.456,
				Rate:     78.9,
			},
		},
		{
			name: "arrays_and_slices",
			data: struct {
				Tags    []string `json:"tags"`
				Numbers []int    `json:"numbers"`
				Binary  [][]byte `json:"binary"`
			}{
				Tags:    []string{"tag1", "tag2", "tag3"},
				Numbers: []int{1, 2, 3, 4, 5},
				Binary:  [][]byte{[]byte("first"), []byte("second")},
			},
		},
		{
			name: "map_types",
			data: struct {
				StringMap map[string]string `json:"stringMap"`
				IntMap    map[string]int    `json:"intMap"`
				ByteMap   map[string][]byte `json:"byteMap"`
			}{
				StringMap: map[string]string{"key1": "value1", "key2": "value2"},
				IntMap:    map[string]int{"count": 10, "limit": 100},
				ByteMap:   map[string][]byte{"data1": []byte("content1"), "data2": []byte("content2")},
			},
		},
		{
			name: "nested_struct",
			data: struct {
				Info struct {
					Name string `json:"name"`
					Data []byte `json:"data"`
				} `json:"info"`
				Meta map[string]interface{} `json:"meta"`
			}{
				Info: struct {
					Name string `json:"name"`
					Data []byte `json:"data"`
				}{
					Name: "nested",
					Data: []byte("nested data"),
				},
				Meta: map[string]interface{}{
					"version": 1,
					"active":  true,
					"config":  []byte("config data"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			marshalResult, err = json.Marshal(tt.data)
			if err != nil {
				t.Fatalf("Failed to marshal %s: %v", tt.name, err)
			}
		})
	}
}
