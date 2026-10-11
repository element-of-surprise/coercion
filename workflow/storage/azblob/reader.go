package azblob

import (
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/google/uuid"
	"github.com/gostdlib/base/concurrency/sync"
	"github.com/gostdlib/base/context"
	"github.com/gostdlib/base/values/chans"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/plugins/registry"
	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/errors"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/blobops"
	"github.com/element-of-surprise/coercion/workflow/storage/azblob/internal/planlocks"
	"github.com/element-of-surprise/coercion/workflow/utils/clone"
)

var _ storage.Reader = reader{}

// reader implements the storage.Reader interface.
type reader struct {
	mu            *planlocks.Group
	readFlight    *sync.Flight[string, *workflow.Plan]
	existsFlight  *sync.Flight[string, bool]
	prefix        string
	client        blobops.Ops
	reg           *registry.Register
	retentionDays int
	nowf          func() time.Time
	pools         fetchPools

	testListPlansInContainer func(ctx context.Context, containerName string) ([]storage.ListResult, error)

	private.Storage
}

func (r reader) now() time.Time {
	if r.nowf == nil {
		return time.Now()
	}
	return r.nowf()
}

// Exists implements storage.Reader.Exists(). It returns true if the plan exists.
func (r reader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	return sharedFetch(ctx, sharedFetchArgs[bool]{
		flight:  r.existsFlight,
		key:     id.String(),
		lock:    r.readLock(id),
		fetch:   func(ctx context.Context) (bool, error) { return r.exists(ctx, id) },
		timeout: sharedFetchTimeout,
	})
}

// readLock returns a lock func for sharedFetchArgs that takes id's plan read lock.
func (r reader) readLock(id uuid.UUID) func() (unlock func()) {
	return func() (unlock func()) {
		r.mu.RLock(id)
		return func() { r.mu.RUnlock(id) }
	}
}

// sharedFetchTimeout bounds a shared Read or Exists fetch, which no caller can cancel. A fetch is a handful of blob
// requests; this leaves room for each to ride out several minutes of throttling, while keeping a stalled request from
// holding the plan's read lock, and so blocking every writer of the plan, much longer than that.
const sharedFetchTimeout = 10 * time.Minute

// sharedFetchArgs are the arguments to sharedFetch.
type sharedFetchArgs[V any] struct {
	// flight shares the fetch between callers asking for key at the same time.
	flight *sync.Flight[string, V]
	key    string
	// lock takes the lock the fetch needs and returns the func that releases it.
	lock func() (unlock func())
	// fetch does the work, holding the lock.
	fetch func(ctx context.Context) (V, error)
	// timeout bounds the fetch, counted from when the lock is held.
	timeout time.Duration
}

func (a sharedFetchArgs[V]) validate() error {
	switch {
	case a.flight == nil:
		return errors.New("flight cannot be nil")
	case a.key == "":
		return errors.New("key cannot be empty")
	case a.lock == nil:
		return errors.New("lock cannot be nil")
	case a.fetch == nil:
		return errors.New("fetch cannot be nil")
	case a.timeout <= 0:
		return errors.New("timeout must be positive")
	}
	return nil
}

// sharedFetch runs fetch once for every caller asking for key at the same time and gives each of them its result.
// Because the fetch is shared, it runs with ctx's values but not its cancellation: one caller giving up must not fail
// the others. Each caller still waits only as long as its own ctx allows, and gets a TypeTimeout error if it gives up
// first. The fetch takes its lock itself, so it stays locked after a caller stops waiting.
func sharedFetch[V any](ctx context.Context, args sharedFetchArgs[V]) (V, error) {
	var zero V
	if err := args.validate(); err != nil {
		return zero, errors.E(ctx, errors.CatInternal, errors.TypeBug, err)
	}
	// A caller that has already given up must not start a fetch: no one would read it, and it would hold the plan's
	// read lock, blocking its writers, until it finished.
	if ctx.Err() != nil {
		return zero, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("stopped waiting for %s: %w", args.key, context.Cause(ctx)))
	}
	results := args.flight.DoChan(ctx, args.key, func() (V, error) {
		unlock := args.lock()
		defer unlock()
		// Runs before unlock: once a writer can take the lock, a new caller must start a new fetch rather than join
		// this one and get what was read before the write.
		defer args.flight.Forget(ctx, args.key)

		// Started once the lock is held, so time spent waiting behind a writer does not use up the fetch's time.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), args.timeout)
		defer cancel()
		return args.fetch(fetchCtx)
	})
	// A result that is ready as ctx ends is still returned.
	res, r := chans.Get(ctx, results)
	if !r.OK() {
		return zero, errors.E(ctx, errors.CatInternal, errors.TypeTimeout, fmt.Errorf("stopped waiting for %s: %w", args.key, context.Cause(ctx)))
	}
	return res.Val, res.Err
}

func (r reader) exists(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := r.fetchPlanEntryMeta(ctx, id)
	if err == nil {
		return true, nil
	}

	if blobops.IsNotFound(err) {
		return false, nil
	}
	return false, errors.E(ctx, errors.CatInternal, errors.TypeStorageGet, fmt.Errorf("failed to check plan existence: %w", err))
}

// Read implements storage.Reader.Read().
func (r reader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	// We have a retention time in blob storage because of unique(read pain in the ass) way of
	// doing storage. So even if something is in our storage still, if it is past retention we just
	// say it is not.
	if time.Unix(id.Time().UnixTime()).Before(r.now().AddDate(0, 0, -r.retentionDays)) {
		return nil, errors.ErrNotFound(ctx, fmt.Errorf("plan(%s) is past retention", id))
	}

	plan, err := sharedFetch(ctx, sharedFetchArgs[*workflow.Plan]{
		flight:  r.readFlight,
		key:     id.String(),
		lock:    r.readLock(id),
		fetch:   func(ctx context.Context) (*workflow.Plan, error) { return r.fetchPlan(ctx, id) },
		timeout: sharedFetchTimeout,
	})
	if err != nil {
		return nil, err
	}
	// Every caller sharing the fetch gets its result, and a caller may run or change the Plan it reads (Start and Resume
	// run it), so each gets its own copy and the fetched Plan is only ever read. The copy keeps state, secrets and each
	// object's Plan ID, which the updaters lock and name blobs by; the registry is set again as a fetch sets it.
	plan = clone.Plan(ctx, plan, clone.WithKeepState(), clone.WithKeepSecrets())
	if err := r.setRegistry(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// ReadDirect reads a plan from storage bypassing the retention check.
// This is intended for testing purposes only.
func (r reader) ReadDirect(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	if !testing.Testing() {
		panic("ReadDirect is only for testing")
	}

	r.mu.RLock(id)
	defer r.mu.RUnlock(id)

	return r.fetchPlan(ctx, id)
}

// Search implements storage.Reader.Search().
func (r reader) Search(ctx context.Context, filters storage.Filters) (chan storage.Stream[storage.ListResult], error) {
	if err := filters.Validate(); err != nil {
		return nil, errors.E(ctx, errors.CatUser, errors.TypeParameter, err)
	}

	ch := make(chan storage.Stream[storage.ListResult], 1)

	// The producer waits on the caller to read each result, so it goes on the default pool, never on a limited pool
	// ctx may carry, where a stream the caller abandoned would hold a slot until ctx ends.
	ok := context.Pool(ctx).Default().Submit(
		ctx,
		func() {
			defer close(ch)
			r.search(ctx, filters, ch)
		},
	)
	if !ok {
		close(ch)
		return ch, errors.E(ctx, errors.CatUser, errors.TypeTimeout, context.Cause(ctx))
	}

	return ch, nil
}

// List implements storage.Reader.List(). It lists recent plans, most recent first.
func (r reader) List(ctx context.Context, limit int) (chan storage.Stream[storage.ListResult], error) {
	ch := make(chan storage.Stream[storage.ListResult], 1)

	// See Search for why this is the default pool.
	ok := context.Pool(ctx).Default().Submit(
		ctx,
		func() {
			defer close(ch)
			r.list(ctx, limit, ch)
		},
	)
	if !ok {
		close(ch)
		return ch, errors.E(ctx, errors.CatUser, errors.TypeTimeout, context.Cause(ctx))
	}

	return ch, nil
}

// search performs the actual search using blob index tags.
func (r reader) search(ctx context.Context, filters storage.Filters, ch chan storage.Stream[storage.ListResult]) {
	// For now, implement search by listing all plans and filtering
	// TODO: When Azure Blob Index Tags search API is available in the SDK, use it for better performance

	containers := searchContainerNames(r.prefix, r.retentionDays)

	var results []storage.ListResult

	for _, containerName := range containers {
		containerResults, err := r.listPlansInContainer(ctx, containerName)
		if err != nil {
			if !blobops.IsNotFound(err) {
				chans.Put(ctx, ch, storage.Stream[storage.ListResult]{Err: err})
				return
			}
			continue
		}

		results = append(results, containerResults...)
	}

	// Filter results based on filters
	for _, result := range results {
		if r.matchesFilters(result, filters) {
			if !chans.Put(ctx, ch, storage.Stream[storage.ListResult]{Result: result}) {
				return
			}
		}
	}
}

// matchesFilters checks if a result matches the given filters.
func (r reader) matchesFilters(result storage.ListResult, filters storage.Filters) bool {
	// Check ID filter
	if len(filters.ByIDs) > 0 {
		if !slices.Contains(filters.ByIDs, result.ID) {
			return false
		}
	}

	// Check GroupID filter
	if len(filters.ByGroupIDs) > 0 {
		if !slices.Contains(filters.ByGroupIDs, result.GroupID) {
			return false
		}
	}

	// Check Status filter
	if len(filters.ByStatus) > 0 {
		if !slices.Contains(filters.ByStatus, result.State.Status) {
			return false
		}
	}

	return true
}

// list lists all plans, most recent first.
func (r reader) list(ctx context.Context, limit int, ch chan storage.Stream[storage.ListResult]) {
	containers := searchContainerNames(r.prefix, r.retentionDays)

	var results []storage.ListResult
	count := 0

	for _, containerName := range containers {
		if limit > 0 && count >= limit {
			break
		}

		containerResults, err := r.listPlansInContainer(ctx, containerName)
		if err != nil {
			if !blobops.IsNotFound(err) {
				chans.Put(ctx, ch, storage.Stream[storage.ListResult]{Err: err})
				return
			}
			continue
		}

		results = append(results, containerResults...)
		count += len(containerResults)
	}

	// Sort by submit time (most recent first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].SubmitTime.After(results[j].SubmitTime)
	})

	// Apply limit if specified
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Send results
	for _, result := range results {
		if !chans.Put(ctx, ch, storage.Stream[storage.ListResult]{Result: result}) {
			return
		}
	}
}

// listPlansInContainer lists all plans in a specific container, using each plan's entry blob.
func (r reader) listPlansInContainer(ctx context.Context, containerName string) ([]storage.ListResult, error) {
	if r.testListPlansInContainer != nil && testing.Testing() {
		return r.testListPlansInContainer(ctx, containerName)
	}

	blobs, err := r.listPlanBlobs(ctx, containerName, true)
	if err != nil {
		return nil, err
	}
	var results []storage.ListResult
	for _, b := range blobs {
		if b.meta != nil {
			results = append(results, b.meta.ListResult)
		}
	}
	return results, nil
}

// planBlob is one entry or object blob found under a container's plans/ directory.
type planBlob struct {
	// id and kind come from the blob's name, so they are known even when its metadata is not.
	id   uuid.UUID
	kind blobKind
	// meta is the blob's parsed metadata, or nil if it could not be parsed.
	meta *planMeta
}

// listPlanBlobs lists every entry and object blob in a container's plans/ directory, or only the entry blobs if
// entriesOnly is set; blobs left out are skipped before their metadata is parsed. A blob whose metadata cannot be
// parsed is still returned, with a nil meta, so callers can tell a plan that has an unreadable blob from one that has
// no blob at all. Blobs whose names are not plan blob names are skipped.
func (r reader) listPlanBlobs(ctx context.Context, containerName string, entriesOnly bool) ([]planBlob, error) {
	pager := r.client.NewListBlobsFlatPager(containerName, &azblob.ListBlobsFlatOptions{
		Prefix:  toPtr(planBlobPrefix()),
		Include: container.ListBlobsInclude{Metadata: true},
	})

	var blobs []planBlob
	for pager.More() {
		page, err := r.client.NextListPage(ctx, pager)
		if err != nil {
			return nil, errors.E(ctx, errors.CatInternal, errors.TypeStorageList, err)
		}

		for _, blob := range page.Segment.BlobItems {
			if blob.Name == nil {
				continue
			}
			id, kind, ok := parsePlanBlobName(*blob.Name)
			if !ok || (entriesOnly && kind != entryBlob) {
				continue
			}
			b := planBlob{id: id, kind: kind}
			pm, err := mapToPlanMeta(blob.Metadata)
			switch {
			case err != nil:
				context.Log(ctx).Error(fmt.Sprintf("could not parse plan metadata for blob(%s): %v", *blob.Name, err))
			case pm.ID != id || pm.PlanType != kind.planType():
				context.Log(ctx).Error(fmt.Sprintf("plan metadata for blob(%s) says plan(%s) type(%s)", *blob.Name, pm.ID, pm.PlanType))
			default:
				b.meta = &pm
			}
			blobs = append(blobs, b)
		}
	}
	return blobs, nil
}
