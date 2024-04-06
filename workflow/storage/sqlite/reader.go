package sqlite

import (
	"context"
	"fmt"
	"unsafe"

	"github.com/element-of-surprise/workstream/plugins/registry"
	"github.com/element-of-surprise/workstream/workflow"
	"github.com/element-of-surprise/workstream/workflow/storage"

	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// planReader implements the storage.PlanReader interface.
type planReader struct {
	conn *sqlite.Conn
	reg  *registry.Register
}

// Exists returns true if the Plan ID exists in the storage.
func (p *planReader) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	const q = "SELECT COUNT(*) FROM 'plans' WHERE 'id' = ?;"

	count := -1
	err := sqlitex.ExecuteTransient(
		p.conn,
		q,
		&sqlitex.ExecOptions{
			Args: []any{
				id[:],
			},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		},
	)
	if err != nil {
		return false, fmt.Errorf("couldn't do a lookup in table plans: %w", err)
	}
	if count < 0 {
		return false, fmt.Errorf("bug: unexpected count value: %d", count)
	}
	return count > 0, nil
}

// ReadPlan returns a Plan from the storage.
func (p *planReader) Read(ctx context.Context, id uuid.UUID) (*workflow.Plan, error) {
	return p.fetchPlan(ctx, id)
}

// SearchPlans returns a list of Plan IDs that match the filter.
func (p *planReader) Search(ctx context.Context, filter storage.SearchFilter) ([]uuid.UUID, error) {
	panic("not implemented") // TODO: Implement
}

// ListPlans returns a list of all Plan IDs in the storage. This should
// return with most recent submiited first.
func (p *planReader) List(ctx context.Context, limit int) ([]uuid.UUID, error) {
	panic("not implemented") // TODO: Implement
}

func (p *planReader) private() {
	panic("not implemented") // TODO: Implement
}

// fieldToID returns a uuid.UUID from a field "field" in the Stmt that must be a TEXT field.
func fieldToID(field string, stmt *sqlite.Stmt) (uuid.UUID, error) {
	return uuid.Parse(stmt.GetText(field))
}

// fieldToIDs returns the IDs from the statement field. Field must the a blob
// encoded as a JSON array that has string UUIDs in v7 format.
func fieldToIDs(field string, stmt *sqlite.Stmt) ([]uuid.UUID, error) {
	contents := fieldToBytes("field", stmt)
	if contents == nil {
		return nil, fmt.Errorf("actions IDs are nil")
	}
	strIDs := []string{}
	if err := json.Unmarshal(contents, &strIDs); err != nil {
		return nil, fmt.Errorf("couldn't unmarshal action ids: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(strIDs))
	for _, id := range strIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("couldn't parse id(%s): %w", id, err)
		}
		ids = append(ids, u)
	}

	return ids, nil
}

func strToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// fieldToBytes returns the bytes of the field from the statement.
func fieldToBytes(field string, stmt *sqlite.Stmt) []byte {
	l := stmt.GetLen(field)
	if l == 0 {
		return nil
	}
	b := make([]byte, l)
	stmt.GetBytes(field, b)
	return b
}
