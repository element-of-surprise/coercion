package sqlite

import (
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite/sqlitex"
)

var _ storage.Updater = updater{}

// updater implements the storage.updater interface.
type updater struct {
	planUpdater
	checksUpdater
	blockUpdater
	sequenceUpdater
	actionUpdater

	private.Storage
}

func newUpdater(mu *sync.Mutex, pool *sqlitex.Pool) updater {
	return updater{
		planUpdater:     planUpdater{mu: mu, pool: pool},
		checksUpdater:   checksUpdater{mu: mu, pool: pool},
		blockUpdater:    blockUpdater{mu: mu, pool: pool},
		sequenceUpdater: sequenceUpdater{mu: mu, pool: pool},
		actionUpdater:   actionUpdater{mu: mu, pool: pool},
	}
}
