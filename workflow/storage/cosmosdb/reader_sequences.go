package cosmosdb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// idsToSequences converts the "sequences" field in a cosmosdb document to a list of *workflow.Sequences.
func (p reader) idsToSequences(ctx context.Context, sequenceIDs []uuid.UUID) ([]*workflow.Sequence, error) {
	sequences := make([]*workflow.Sequence, 0, len(sequenceIDs))
	for _, id := range sequenceIDs {
		sequence, err := p.fetchSequenceByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("couldn't fetch sequence(%s)by id: %w", id, err)
		}
		sequences = append(sequences, sequence)
	}
	return sequences, nil
}

// fetchSequenceByID fetches a sequence by its id.
func (p reader) fetchSequenceByID(ctx context.Context, id uuid.UUID) (*workflow.Sequence, error) {
	res, err := p.client.ReadItem(ctx, p.pk, id.String(), p.defaultIOpts)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch sequence by id: %w", err)
	}
	return p.docToSequence(ctx, &res)
}

// docToSequence converts a cosmosdb document to a *workflow.Sequence.
func (p reader) docToSequence(ctx context.Context, response *azcosmos.ItemResponse) (*workflow.Sequence, error) {
	var err error
	var resp sequencesEntry
	if err = json.Unmarshal(response.Value, &resp); err != nil {
		return nil, err
	}

	s := &workflow.Sequence{
		ID:    resp.ID,
		Key:   resp.Key,
		Name:  resp.Name,
		Descr: resp.Descr,
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
	}
	s.Actions, err = p.idsToActions(ctx, resp.Actions)
	if err != nil {
		return nil, fmt.Errorf("couldn't read sequence actions: %w", err)
	}

	return s, nil
}
