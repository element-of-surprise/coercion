package cosmosdb

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/retry/exponential"
	"github.com/gostdlib/base/values/sizes"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/utils/changes"
	"github.com/element-of-surprise/coercion/workflow/utils/walk"
)

// maxBatchOps is the most operations Cosmos DB accepts in one transactional batch.
const maxBatchOps = 100

// maxBatchBytes is the most operation bytes put in one transactional batch. Cosmos DB rejects a batch request over
// 2 MiB; the 64 KiB left over covers the request's JSON array and anything the SDK adds around the operations.
const maxBatchBytes = 2*sizes.MiB - 64*sizes.KiB

// batchOpOverhead is the JSON the SDK wraps around each patch in a batch, not counting its ID, ETag and patch:
// {"operationType":"Patch","ifMatch":"","id":"","resourceBody":} plus the comma between operations.
const batchOpOverhead = len(`{"operationType":"Patch","ifMatch":"","id":"","resourceBody":}`) + 1

var _ storage.ChangesUpdater = changesUpdater{}

// changesUpdater implements storage.ChangesUpdater.
type changesUpdater struct {
	mu     *sync.RWMutex
	client creatorClient

	private.Storage
}

// UpdateChanges implements storage.ChangesUpdater.UpdateChanges(). Every document of a Plan shares its partition key,
// so the changed objects are patched in transactional batches of up to maxBatchOps operations and maxBatchBytes: each
// batch is stored whole or not at all. Batches are written in order with every object after all of its descendants,
// so if a later batch fails, no parent has been stored over children that were not.
func (u changesUpdater) UpdateChanges(ctx context.Context, plan *workflow.Plan, before changes.Snapshot) error {
	items := changes.Since(plan, before)
	if len(items) == 0 {
		return nil
	}
	slices.Reverse(items)

	batches, err := batchOps(items)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeBug, fmt.Errorf("plan(%s): %w", plan.ID, err))
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	for _, ops := range batches {
		if err := u.patchBatch(ctx, plan.ID.String(), ops); err != nil {
			return errors.E(ctx, errors.CatInternal, errors.TypeStorageUpdate, fmt.Errorf("plan(%s): %w", plan.ID, err))
		}
	}
	return nil
}

// patchOp is one object's patch in a transactional batch.
type patchOp struct {
	item  walk.Item
	patch azcosmos.PatchOperations
	// size is the operation's bytes in the batch request.
	size int
}

// batchOps returns the patches for items, in order, grouped into batches of at most maxBatchOps operations and
// maxBatchBytes. An operation larger than maxBatchBytes on its own gets a batch of its own.
func batchOps(items []walk.Item) ([][]patchOp, error) {
	var batches [][]patchOp
	var cur []patchOp
	curBytes := 0
	for _, item := range items {
		patch, err := objectPatch(item)
		if err != nil {
			return nil, err
		}
		b, err := patch.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("couldn't size the patch of %v: %w", item.Value.Type(), err)
		}
		obj := item.Value.(stateIDer)
		size := len(b) + len(obj.GetID().String()) + len(obj.GetState().ETag) + batchOpOverhead

		if len(cur) == maxBatchOps || (len(cur) > 0 && curBytes+size > maxBatchBytes) {
			batches = append(batches, cur)
			cur, curBytes = nil, 0
		}
		cur = append(cur, patchOp{item: item, patch: patch, size: size})
		curBytes += size
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches, nil
}

// patchBatch patches ops in one transactional batch and records each document's new ETag on its object.
func (u changesUpdater) patchBatch(ctx context.Context, planID string, ops []patchOp) error {
	batch := u.client.NewTransactionalBatch(key(planID))
	for _, op := range ops {
		obj := op.item.Value.(stateIDer)
		batch.PatchItem(obj.GetID().String(), op.patch, &azcosmos.TransactionalBatchItemOptions{IfMatchETag: ifMatch(obj.GetState())})
	}

	var resp azcosmos.TransactionalBatchResponse
	op := func(ctx context.Context, r exponential.Record) error {
		var err error
		resp, err = u.client.ExecuteTransactionalBatch(ctx, batch, emptyBatchOptions)
		if err != nil {
			if !isRetriableError(err) {
				return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
			}
			return err
		}
		return batchErr(resp)
	}
	if err := backoff.Retry(context.WithoutCancel(ctx), op); err != nil {
		return fmt.Errorf("failed to patch objects through Cosmos DB API: %w", err)
	}

	for i, result := range resp.OperationResults {
		if i >= len(ops) {
			break
		}
		obj := ops[i].item.Value.(stateIDer)
		state := obj.GetState()
		state.ETag = string(result.ETag)
		obj.SetState(state)
	}
	return nil
}

// batchErr returns nil if every operation in a transactional batch succeeded. Otherwise it reports the operation that
// failed (the others fail with 424 Failed Dependency) and marks it permanent unless retrying could help.
func batchErr(resp azcosmos.TransactionalBatchResponse) error {
	if resp.Success {
		return nil
	}
	for i, result := range resp.OperationResults {
		if result.StatusCode == http.StatusFailedDependency {
			continue
		}
		err := fmt.Errorf("batch operation(%d) failed with status code %d", i, result.StatusCode)
		if retriableStatus(result.StatusCode) {
			return err
		}
		return fmt.Errorf("%w: %w", err, exponential.ErrPermanent)
	}
	return fmt.Errorf("transactional batch failed without an operation result: %w", exponential.ErrPermanent)
}

// retriableStatus reports whether a failed batch operation's status code is one that retrying can fix: throttling, a
// timeout or a server error.
func retriableStatus(code int32) bool {
	return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= http.StatusInternalServerError
}

// stateIDer is an object below a Plan whose state is stored in its own document.
type stateIDer interface {
	walk.Stateful
	GetID() uuid.UUID
}

// objectPatch returns the patch that writes one object's state (and, for an Action, its attempts).
func objectPatch(item walk.Item) (azcosmos.PatchOperations, error) {
	state := item.Value.(walk.Stateful).GetState()
	patch := azcosmos.PatchOperations{}
	patch.AppendReplace("/stateStatus", state.Status)
	patch.AppendReplace("/stateStart", state.Start)
	patch.AppendReplace("/stateEnd", state.End)

	switch item.Value.Type() {
	case workflow.OTCheck, workflow.OTBlock, workflow.OTSequence, workflow.OTDeferredActions, workflow.OTBatch:
	case workflow.OTAction:
		attempts, err := encodeAttempts(item.Action().Attempts.Get())
		if err != nil {
			return azcosmos.PatchOperations{}, err
		}
		patch.AppendSet("/attempts", attempts)
	default:
		return azcosmos.PatchOperations{}, fmt.Errorf("cannot write object of type %v", item.Value.Type())
	}
	return patch, nil
}

// ifMatch returns the ETag a patch must match, or nil if the object has none.
func ifMatch(state workflow.State) *azcore.ETag {
	if state.ETag == "" {
		return nil
	}
	etag := azcore.ETag(state.ETag)
	return &etag
}
