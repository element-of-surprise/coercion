package sqlite

import (
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite"
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

func newUpdater(mu *sync.Mutex, conn *sqlite.Conn) updater {
	return updater{
		planUpdater:     planUpdater{mu: mu, conn: conn},
		checksUpdater:   checksUpdater{mu: mu, conn: conn},
		blockUpdater:    blockUpdater{mu: mu, conn: conn},
		sequenceUpdater: sequenceUpdater{mu: mu, conn: conn},
		actionUpdater:   actionUpdater{mu: mu, conn: conn},
	}
}
