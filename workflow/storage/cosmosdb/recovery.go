package cosmosdb

import (
	"fmt"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"github.com/gostdlib/base/context"
)

type recovery struct {
	reader  storage.Reader
	updater storage.Updater
}

// Recovery implements storage.Recovery. There is a case where when we write our search entries
// an update could be missed because writing the object and the search entry is not atomic.
// The service could die between these operations. This method is used to recover from that case.
func (r recovery) Recovery(ctx context.Context) error {
	stream, err := r.reader.Search(ctx, storage.Filters{ByStatus: []workflow.Status{workflow.Running}})
	if err != nil {
		return err
	}
	for entry := range stream {
		if entry.Err != nil {
			return entry.Err
		}
		p, err := r.reader.Read(ctx, entry.Result.ID)
		if err != nil {
			return fmt.Errorf("failed to locate entry %v that is in the search stream", entry.Result.ID)
		}
		if err := r.updater.UpdatePlan(ctx, p); err != nil {
			return fmt.Errorf("failed to update plan %v", p.ID)
		}
	}
	return nil
}
