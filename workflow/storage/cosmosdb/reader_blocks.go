package cosmosdb

import (
	"fmt"

	"github.com/gostdlib/base/context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// idsToBlocks converts the "blocks" field in a cosmosdb document to a list of *workflow.Blocks.
func (p reader) idsToBlocks(ctx context.Context, planID azcosmos.PartitionKey, blockIDs []uuid.UUID) ([]*workflow.Block, error) {
	blocks := make([]*workflow.Block, 0, len(blockIDs))
	for _, id := range blockIDs {
		block, err := p.fetchBlockByID(ctx, planID, id)
		if err != nil {
			return nil, fmt.Errorf("couldn't fetch block(%s)by id: %w", id, err)
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// fetchBlockByID fetches a block by its id.
func (p reader) fetchBlockByID(ctx context.Context, planID azcosmos.PartitionKey, id uuid.UUID) (*workflow.Block, error) {
	res, err := p.client.ReadItem(ctx, planID, id.String(), p.defaultIOpts)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch block by id: %w", err)
	}

	return p.docToBlock(ctx, &res)
}

// docToBlock converts a cosmosdb document to a *workflow.Block.
func (p reader) docToBlock(ctx context.Context, response *azcosmos.ItemResponse) (*workflow.Block, error) {
	var err error
	var resp blocksEntry
	if err = json.Unmarshal(response.Value, &resp); err != nil {
		return nil, err
	}

	b := &workflow.Block{
		ID:            resp.ID,
		Key:           resp.Key,
		Name:          resp.Name,
		Descr:         resp.Descr,
		EntranceDelay: resp.EntranceDelay,
		ExitDelay:     resp.ExitDelay,
		State: &workflow.State{
			Status: resp.StateStatus,
			Start:  resp.StateStart,
			End:    resp.StateEnd,
			ETag:   string(resp.ETag),
		},
		Concurrency:       resp.Concurrency,
		ToleratedFailures: resp.ToleratedFailures,
	}
	b.SetPlanID(resp.PlanID)

	k := key(resp.PlanID)
	b.BypassChecks, err = p.idToCheck(ctx, k, resp.BypassChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get block bypasschecks: %w", err)
	}
	b.PreChecks, err = p.idToCheck(ctx, k, resp.PreChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get block prechecks: %w", err)
	}
	b.ContChecks, err = p.idToCheck(ctx, k, resp.ContChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get block contchecks: %w", err)
	}
	b.PostChecks, err = p.idToCheck(ctx, k, resp.PostChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get block postchecks: %w", err)
	}
	b.DeferredChecks, err = p.idToCheck(ctx, k, resp.DeferredChecks)
	if err != nil {
		return nil, fmt.Errorf("couldn't get block deferredchecks: %w", err)
	}
	b.Sequences, err = p.idsToSequences(ctx, k, resp.Sequences)
	if err != nil {
		return nil, fmt.Errorf("couldn't read block sequences: %w", err)
	}

	return b, nil
}
