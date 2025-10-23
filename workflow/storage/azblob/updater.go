package azblob

import (
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
)

var _ storage.Updater = updater{}

// updater implements the storage.Updater interface.
type updater struct {
	planUpdater
	checksUpdater
	blockUpdater
	sequenceUpdater
	actionUpdater

	private.Storage
}

func newUpdater(mu *sync.RWMutex, prefix string, client *azblob.Client, endpoint string) updater {
	u := updater{}

	u.planUpdater = planUpdater{
		mu:       mu,
		prefix:   prefix,
		client:   client,
		endpoint: endpoint,
	}
	u.checksUpdater = checksUpdater{
		mu:       mu,
		prefix:   prefix,
		client:   client,
		endpoint: endpoint,
	}
	u.blockUpdater = blockUpdater{
		mu:       mu,
		prefix:   prefix,
		client:   client,
		endpoint: endpoint,
	}
	u.sequenceUpdater = sequenceUpdater{
		mu:       mu,
		prefix:   prefix,
		client:   client,
		endpoint: endpoint,
	}
	u.actionUpdater = actionUpdater{
		mu:       mu,
		prefix:   prefix,
		client:   client,
		endpoint: endpoint,
	}

	return u
}

// planUpdater implements storage.PlanUpdater.
type planUpdater struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	endpoint string

	private.Storage
}

// UpdatePlan implements storage.PlanUpdater.UpdatePlan().
// Updates only the planEntry blob (lightweight, IDs only) and metadata during execution.
// Does NOT update the workflow.Plan object blob.
func (u planUpdater) UpdatePlan(ctx context.Context, plan *workflow.Plan) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Find the container where the plan exists
	containerName := containerForPlan(u.prefix, plan.ID)

	// Convert plan to lightweight planEntry
	planEntry, err := planToPlanEntry(plan)
	if err != nil {
		return errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	// Marshal state for metadata
	stateJSON, err := json.Marshal(plan.State)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal plan state: %w", err))
	}

	// Update metadata
	md := map[string]*string{
		mdKeyPlanID:     toPtr(plan.ID.String()),
		mdKeyName:       toPtr(plan.Name),
		mdKeyDescr:      toPtr(plan.Descr),
		mdKeySubmitTime: toPtr(plan.SubmitTime.Format(time.RFC3339Nano)),
		mdKeyState:      toPtr(bytesToStr(stateJSON)),
	}

	if plan.GroupID != uuid.Nil {
		md[mdKeyGroupID] = toPtr(plan.GroupID.String())
	}

	// Marshal planEntry
	data, err := json.Marshal(planEntry)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal planEntry: %w", err))
	}

	// Upload updated planEntry blob with metadata
	blobName := planEntryBlobName(plan.ID)
	if err := uploadBlob(ctx, u.client, containerName, blobName, md, data); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to update planEntry blob: %w", err))
	}

	return nil
}

// blocksUpdater implements storage.BlockUpdater.
type blockUpdater struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	creator  creator
	endpoint string

	private.Storage
}

// UpdateBlock implements storage.BlockUpdater.UpdateBlock().
func (u blockUpdater) UpdateBlock(ctx context.Context, block *workflow.Block) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.updateObject(ctx, block, func(pos int) ([]byte, error) {
		entry, err := blockToEntry(block, pos)
		if err != nil {
			return nil, err
		}
		return json.Marshal(entry)
	})
}

// checksUpdater implements storage.ChecksUpdater.
type checksUpdater struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	endpoint string

	private.Storage
}

// UpdateChecks implements storage.ChecksUpdater.UpdateChecks().
func (u checksUpdater) UpdateChecks(ctx context.Context, checks *workflow.Checks) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.updateObject(ctx, checks, func(pos int) ([]byte, error) {
		entry, err := checksToEntry(checks)
		if err != nil {
			return nil, err
		}
		return json.Marshal(entry)
	})
}

// sequenceUpdater implements storage.SequenceUpdater.
type sequenceUpdater struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	endpoint string

	private.Storage
}

// UpdateSequence implements storage.SequenceUpdater.UpdateSequence().
func (u sequenceUpdater) UpdateSequence(ctx context.Context, seq *workflow.Sequence) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.updateObject(ctx, seq, func(pos int) ([]byte, error) {
		entry, err := sequenceToEntry(seq, pos)
		if err != nil {
			return nil, err
		}
		return json.Marshal(entry)
	})
}

// actionUpdater implements storage.ActionUpdater.
type actionUpdater struct {
	mu       *sync.RWMutex
	prefix   string
	client   *azblob.Client
	endpoint string

	private.Storage
}

// UpdateAction implements storage.ActionUpdater.UpdateAction().
func (u actionUpdater) UpdateAction(ctx context.Context, action *workflow.Action) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.updateObject(ctx, action, func(pos int) ([]byte, error) {
		entry, err := actionToEntry(action, pos)
		if err != nil {
			return nil, err
		}
		return json.Marshal(entry)
	})
}

// updateObject is a generic helper for updating any workflow object.
func (u blockUpdater) updateObject(ctx context.Context, obj workflow.Object, marshal func(int) ([]byte, error)) error {
	// Find the container where the object exists
	containerName, err := findObjectContainer(u.prefix, obj)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to find object container: %w", err))
	}

	// Marshal the object (pos=0 since we don't track position on updates)
	data, err := marshal(0)
	if err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to marshal object: %w", err))
	}

	// Upload updated blob
	blobName := blobNameForObject(obj)
	if err := uploadBlob(ctx, u.client, containerName, blobName, nil, data); err != nil {
		return errors.E(ctx, errors.CatInternal, errors.TypeStoragePut, fmt.Errorf("failed to update object blob: %w", err))
	}

	return nil
}

// Similar updateObject methods for other updaters
func (u checksUpdater) updateObject(ctx context.Context, obj workflow.Object, marshal func(int) ([]byte, error)) error {
	return blockUpdater{mu: u.mu, prefix: u.prefix, client: u.client, endpoint: u.endpoint}.updateObject(ctx, obj, marshal)
}

func (u sequenceUpdater) updateObject(ctx context.Context, obj workflow.Object, marshal func(int) ([]byte, error)) error {
	return blockUpdater{mu: u.mu, prefix: u.prefix, client: u.client, endpoint: u.endpoint}.updateObject(ctx, obj, marshal)
}

func (u actionUpdater) updateObject(ctx context.Context, obj workflow.Object, marshal func(int) ([]byte, error)) error {
	return blockUpdater{mu: u.mu, prefix: u.prefix, client: u.client, endpoint: u.endpoint}.updateObject(ctx, obj, marshal)
}
