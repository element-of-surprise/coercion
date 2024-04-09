package sqlite

import (
	"sync"

	"github.com/element-of-surprise/workstream/internal/private"
	"zombiezen.com/go/sqlite"
)

// updater implements the storage.updater interface.
type updater struct {
	checksUpdater
	blockUpdater
	sequenceUpdater
	actionUpdater

	private.Storage
}

func newUpdater(mu *sync.Mutex, conn *sqlite.Conn) updater {
	return updater{
		checksUpdater:   checksUpdater{mu: mu, conn: conn},
		blockUpdater:    blockUpdater{mu: mu, conn: conn},
		sequenceUpdater: sequenceUpdater{mu: mu, conn: conn},
		actionUpdater:   actionUpdater{mu: mu, conn: conn},
	}
}
