package cosmosdb

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/plugins"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/gostdlib/base/retry/exponential"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

// TODO: Optimize memory by using json.MarshalWrite and recycling buffers (might require changes to upstream).

var zeroTime = time.Time{}
var emptyItemOptions = &azcosmos.TransactionalBatchItemOptions{}
var emptyBatchOptions = &azcosmos.TransactionalBatchOptions{}

// commitPlan commits a plan to the database. This commits the entire plan and all sub-objects.
func (c creator) commitPlan(ctx context.Context, p *workflow.Plan) (err error) {
	if p == nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeParameter, fmt.Errorf("commitPlan: plan cannot be nil"))
	}

	itemContext, err := planToItems(c.swarm, p)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	se, err := planToSearchEntry(c.swarm, p)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	// Commit to our plan collection.
	batch := c.client.NewTransactionalBatch(key(p))
	for _, item := range itemContext.items {
		batch.CreateItem(item, emptyItemOptions)
	}

	if err := backoff.Retry(ctx, batchRetryer(batch, c.client)); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to commit plan: %w", err))

	}

	// need to re-read plan, because batch response does not contain ETag for each item.
	result, err := c.reader.fetchPlan(ctx, p.ID)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to fetch plan: %w", err))
	}
	*p = *result

	// Commit to our search collection.
	batch = c.client.NewTransactionalBatch(searchKey)
	b, err := json.Marshal(se)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal search record: %w", err))
	}
	batch.CreateItem(b, emptyItemOptions)
	if err := backoff.Retry(ctx, batchRetryer(batch, c.client)); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to commit plan to search records: %w", err))
	}

	return nil
}

// batchRetryer returns a retry function that retries the batch operation.
func batchRetryer(batch azcosmos.TransactionalBatch, client creatorClient) exponential.Op {
	return func(ctx context.Context, r exponential.Record) error {
		results, err := client.ExecuteTransactionalBatch(ctx, batch, emptyBatchOptions)
		if err != nil {
			if !isRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		for i, result := range results.OperationResults {
			if result.StatusCode != http.StatusCreated {
				return fmt.Errorf("item(%d) has status code %d: %w", i, result.StatusCode, exponential.ErrPermanent)
			}
		}
		return nil
	}
}

// planToSearchEntry converts a plan to a searchEntry.
func planToSearchEntry(swarm string, p *workflow.Plan) (searchEntry, error) {
	if p == nil {
		return searchEntry{}, fmt.Errorf("planToSearchEntry: plan cannot be nil")
	}

	// Super basic defense in depth check.
	if p.ID == uuid.Nil {
		return searchEntry{}, errors.New("plan must have an ID")
	}

	return searchEntry{
		PartitionKey: searchKeyStr,
		Swarm:        swarm,
		Name:         p.Name,
		Descr:        p.Descr,
		ID:           p.ID,
		GroupID:      p.GroupID,
		StateStatus:  p.State.Status,
		SubmitTime:   p.SubmitTime,
		StateStart:   p.State.Start,
		StateEnd:     p.State.End,
	}, nil
}

type itemsContext struct {
	swarm  string
	planID uuid.UUID
	m      map[string][]byte
	items  [][]byte
}

func planToItems(swarm string, p *workflow.Plan) (*itemsContext, error) {
	iCtx := &itemsContext{
		swarm:  swarm,
		planID: p.GetID(),
		m:      map[string][]byte{},
	}

	plan, err := planToEntry(swarm, p)
	if err != nil {
		return nil, err
	}

	for _, check := range [5]*workflow.Checks{p.BypassChecks, p.PreChecks, p.PostChecks, p.ContChecks, p.DeferredChecks} {
		err = checksToItems(iCtx, check)
		if err != nil {
			return nil, fmt.Errorf("planToItems(commitChecks): %w", err)
		}
	}

	if p.Blocks == nil {
		return nil, fmt.Errorf("commitPlan: plan.Blocks cannot be nil")
	}
	for i, b := range p.Blocks {
		err = blockToItem(iCtx, i, b)
		if err != nil {
			return nil, fmt.Errorf("planToItems(commitBlocks): %w", err)
		}
	}

	item, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal item: %w", err)
	}

	iCtx.m[p.ID.String()] = item
	iCtx.items = append(iCtx.items, item)
	return iCtx, nil
}

func planToEntry(swarm string, p *workflow.Plan) (plansEntry, error) {
	if p == nil {
		return plansEntry{}, fmt.Errorf("planToEntry: plan cannot be nil")
	}

	// Super basic defense in depth check.
	if p.ID == uuid.Nil {
		return plansEntry{}, errors.New("plan must have an ID")
	}

	blocks, err := objsToIDs(p.Blocks)
	if err != nil {
		return plansEntry{}, fmt.Errorf("planToEntry(objsToIDs(blocks)): %w", err)
	}

	plan := plansEntry{
		PartitionKey: keyStr(p.ID),
		Swarm:        swarm,
		Type:         workflow.OTPlan,
		ID:           p.ID,
		PlanID:       p.ID,
		GroupID:      p.GroupID,
		Name:         p.Name,
		Descr:        p.Descr,
		Meta:         p.Meta,
		Blocks:       blocks,
		StateStatus:  p.State.Status,
		StateStart:   p.State.Start,
		StateEnd:     p.State.End,
		Reason:       p.Reason,
	}

	if p.BypassChecks != nil {
		plan.BypassChecks = p.BypassChecks.ID
	}
	if p.PreChecks != nil {
		plan.PreChecks = p.PreChecks.ID
	}
	if p.PostChecks != nil {
		plan.PostChecks = p.PostChecks.ID
	}
	if p.ContChecks != nil {
		plan.ContChecks = p.ContChecks.ID
	}
	if p.DeferredChecks != nil {
		plan.DeferredChecks = p.DeferredChecks.ID
	}
	plan.SubmitTime = p.SubmitTime

	return plan, nil
}

func checksToItems(iCtx *itemsContext, ch *workflow.Checks) error {
	if ch == nil {
		return nil
	}

	checks, err := checkToEntry(iCtx, ch)
	if err != nil {
		return err
	}

	if ch.Actions == nil {
		return fmt.Errorf("commitChecks: checks.Actions cannot be nil")
	}
	for i, a := range ch.Actions {
		if err := actionToItems(iCtx, i, a); err != nil {
			return fmt.Errorf("commitAction: %w", err)
		}
	}
	item, err := json.Marshal(checks)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}
	iCtx.items = append(iCtx.items, item)
	iCtx.m[ch.ID.String()] = item
	return nil
}

func checkToEntry(iCtx *itemsContext, c *workflow.Checks) (checksEntry, error) {
	if c == nil {
		return checksEntry{}, nil
	}

	actions, err := objsToIDs(c.Actions)
	if err != nil {
		return checksEntry{}, fmt.Errorf("objsToIDs(checks.Actions): %w", err)
	}
	return checksEntry{
		PartitionKey: keyStr(iCtx.planID),
		Swarm:        iCtx.swarm,
		Type:         workflow.OTCheck,
		ID:           c.ID,
		Key:          c.Key,
		PlanID:       iCtx.planID,
		Actions:      actions,
		Delay:        c.Delay,
		StateStatus:  c.State.Status,
		StateStart:   c.State.Start,
		StateEnd:     c.State.End,
	}, nil
}

func blockToItem(iCtx *itemsContext, pos int, b *workflow.Block) error {
	if b == nil {
		return fmt.Errorf("commitBlock: block cannot be nil")
	}

	block, err := blockToEntry(iCtx, pos, b)
	if err != nil {
		return err
	}

	for _, check := range [5]*workflow.Checks{b.BypassChecks, b.PreChecks, b.PostChecks, b.ContChecks, b.DeferredChecks} {
		if err = checksToItems(iCtx, check); err != nil {
			return fmt.Errorf("commitBlock(commitChecks): %w", err)
		}
	}

	if b.Sequences == nil {
		return fmt.Errorf("commitBlock: block.Sequences cannot be nil")
	}
	for i, seq := range b.Sequences {
		if err := seqToItems(iCtx, i, seq); err != nil {
			return fmt.Errorf("(commitSequence: %w", err)
		}
	}
	item, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}
	iCtx.items = append(iCtx.items, item)
	iCtx.m[b.ID.String()] = item

	return nil
}

func blockToEntry(iCtx *itemsContext, pos int, b *workflow.Block) (blocksEntry, error) {
	if b == nil {
		return blocksEntry{}, fmt.Errorf("blockToEntry: block cannot be nil")
	}

	sequences, err := objsToIDs(b.Sequences)
	if err != nil {
		return blocksEntry{}, fmt.Errorf("objsToIDs(sequences): %w", err)
	}

	block := blocksEntry{
		PartitionKey:      keyStr(iCtx.planID),
		Swarm:             iCtx.swarm,
		Type:              workflow.OTBlock,
		ID:                b.ID,
		Key:               b.Key,
		PlanID:            iCtx.planID,
		Name:              b.Name,
		Descr:             b.Descr,
		Pos:               pos,
		EntranceDelay:     b.EntranceDelay,
		ExitDelay:         b.ExitDelay,
		Sequences:         sequences,
		Concurrency:       b.Concurrency,
		ToleratedFailures: b.ToleratedFailures,
		StateStatus:       b.State.Status,
		StateStart:        b.State.Start,
		StateEnd:          b.State.End,
	}

	if b.BypassChecks != nil {
		block.BypassChecks = b.BypassChecks.ID
	}
	if b.PreChecks != nil {
		block.PreChecks = b.PreChecks.ID
	}
	if b.PostChecks != nil {
		block.PostChecks = b.PostChecks.ID
	}
	if b.ContChecks != nil {
		block.ContChecks = b.ContChecks.ID
	}
	if b.DeferredChecks != nil {
		block.DeferredChecks = b.DeferredChecks.ID
	}
	return block, nil
}

func seqToItems(iCtx *itemsContext, pos int, seq *workflow.Sequence) error {
	if seq == nil {
		return fmt.Errorf("commitSequence: sequence cannot be nil")
	}

	sequence, err := sequenceToEntry(iCtx, pos, seq)
	if err != nil {
		return err
	}

	if seq.Actions == nil {
		return fmt.Errorf("commitSequence: sequence.Actions cannot be nil")
	}
	for i, a := range seq.Actions {
		if err := actionToItems(iCtx, i, a); err != nil {
			return fmt.Errorf("planToEntry(commitAction): %w", err)
		}
	}
	item, err := json.Marshal(sequence)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}
	iCtx.items = append(iCtx.items, item)
	iCtx.m[seq.ID.String()] = item
	return nil
}

func sequenceToEntry(iCtx *itemsContext, pos int, seq *workflow.Sequence) (sequencesEntry, error) {
	if seq == nil {
		return sequencesEntry{}, fmt.Errorf("sequenceToEntry: sequence cannot be nil")
	}

	actions, err := objsToIDs(seq.Actions)
	if err != nil {
		return sequencesEntry{}, fmt.Errorf("objsToIDs(actions): %w", err)
	}

	return sequencesEntry{
		PartitionKey: keyStr(iCtx.planID),
		Swarm:        iCtx.swarm,
		Type:         workflow.OTSequence,
		ID:           seq.ID,
		Key:          seq.Key,
		PlanID:       iCtx.planID,
		Name:         seq.Name,
		Descr:        seq.Descr,
		Pos:          pos,
		Actions:      actions,
		StateStatus:  seq.State.Status,
		StateStart:   seq.State.Start,
		StateEnd:     seq.State.End,
	}, nil
}

func actionToItems(iCtx *itemsContext, pos int, a *workflow.Action) error {
	if a == nil {
		return fmt.Errorf("commitAction: action cannot be nil")
	}

	action, err := actionToEntry(iCtx, pos, a)
	if err != nil {
		return err
	}

	item, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}
	iCtx.items = append(iCtx.items, item)
	iCtx.m[a.ID.String()] = item

	return nil
}

func actionToEntry(iCtx *itemsContext, pos int, a *workflow.Action) (actionsEntry, error) {
	if a == nil {
		return actionsEntry{}, fmt.Errorf("actionToEntry: action cannot be nil")
	}

	req, err := json.Marshal(a.Req)
	if err != nil {
		return actionsEntry{}, fmt.Errorf("json.Marshal(req): %w", err)
	}
	attempts, err := encodeAttempts(a.Attempts)
	if err != nil {
		return actionsEntry{}, fmt.Errorf("can't encode action.Attempts: %w", err)
	}
	return actionsEntry{
		PartitionKey: keyStr(iCtx.planID),
		Swarm:        iCtx.swarm,
		ID:           a.ID,
		Type:         workflow.OTAction,
		Key:          a.Key,
		PlanID:       iCtx.planID,
		Name:         a.Name,
		Descr:        a.Descr,
		Pos:          pos,
		Plugin:       a.Plugin,
		Timeout:      a.Timeout,
		Retries:      a.Retries,
		Req:          req,
		Attempts:     attempts,
		StateStatus:  a.State.Status,
		StateStart:   a.State.Start,
		StateEnd:     a.State.End,
	}, nil
}

// encodeAttempts encodes a slice of attempts into a JSON array holding JSON encoded attempts as byte slices.
func encodeAttempts(attempts []*workflow.Attempt) ([]byte, error) {
	if len(attempts) == 0 {
		return nil, nil
	}
	var out [][]byte
	if len(attempts) > 0 {
		out = make([][]byte, 0, len(attempts))
		for _, a := range attempts {
			b, err := json.Marshal(a)
			if err != nil {
				return nil, fmt.Errorf("json.Marshal(attempt): %w", err)
			}
			out = append(out, b)
		}
	}
	return json.Marshal(out)
}

// decodeAttempts decodes a JSON array of JSON encoded attempts as byte slices into a slice of attempts.
func decodeAttempts(rawAttempts []byte, plug plugins.Plugin) ([]*workflow.Attempt, error) {
	if rawAttempts == nil {
		return []*workflow.Attempt{}, nil
	}
	rawList := make([][]byte, 0)
	if err := json.Unmarshal(rawAttempts, &rawList); err != nil {
		return nil, fmt.Errorf("json.Unmarshal(rawAttempts): %w", err)
	}

	attempts := make([]*workflow.Attempt, 0, len(rawList))
	for _, raw := range rawList {
		var a = &workflow.Attempt{Resp: plug.Response()}
		if err := json.Unmarshal(raw, a); err != nil {
			return nil, fmt.Errorf("json.Unmarshal(raw): %w", err)
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}

type ider interface {
	GetID() uuid.UUID
}

func objsToIDs[T any](objs []T) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(objs))
	for _, o := range objs {
		if ider, ok := any(o).(ider); ok {
			id := ider.GetID()
			if id == uuid.Nil {
				return nil, fmt.Errorf("objsToIDs: object %T has nil ID", o)
			}
			ids = append(ids, id)
		} else {
			return nil, fmt.Errorf("objsToIDs: object %T does not implement ider", o)
		}
	}
	return ids, nil
}
