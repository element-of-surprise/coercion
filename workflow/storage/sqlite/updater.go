package sqlite

import (
	"fmt"
	"strings"
	"sync"

	"github.com/element-of-surprise/coercion/internal/private"
	"github.com/element-of-surprise/coercion/workflow/storage"
	"zombiezen.com/go/sqlite"
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

func newUpdater(mu *sync.Mutex, pool *sqlitex.Pool, capture *CaptureStmts) updater {
	return updater{
		planUpdater:     planUpdater{mu: mu, pool: pool, capture: capture},
		checksUpdater:   checksUpdater{mu: mu, pool: pool, capture: capture},
		blockUpdater:    blockUpdater{mu: mu, pool: pool, capture: capture},
		sequenceUpdater: sequenceUpdater{mu: mu, pool: pool, capture: capture},
		actionUpdater:   actionUpdater{mu: mu, pool: pool, capture: capture},
	}
}

type kv[T any] struct {
	k string
	v T
}

func (k kv[T]) String() string {
	return fmt.Sprintf("{'%s':'%v'}", k.k, k.v)
}

// Stmt is used to construct a prepared statement. The wrapper allows us to
// capture the parameters passed to the statement. Calling Prepare() will
// return a *sqlite.Stmt that can be executed.
type Stmt struct {
	q      string
	text   []kv[string]
	_int64 []kv[int64]
	_null  []string
	_bytes []kv[[]byte]
	_bool  []kv[bool]
	_float []kv[float64]
}

// Prepare returns a prepared statement that can executed. It will attach all
// the parameters that were set on the Stmt.
func (s *Stmt) Prepare(c *sqlite.Conn) (*sqlite.Stmt, error) {
	stmt, err := c.Prepare(s.q)
	if err != nil {
		return nil, err
	}
	for _, kv := range s.text {
		stmt.SetText(kv.k, kv.v)
	}
	for _, kv := range s._int64 {
		stmt.SetInt64(kv.k, kv.v)
	}
	for _, k := range s._null {
		stmt.SetNull(k)
	}
	for _, kv := range s._bytes {
		stmt.SetBytes(kv.k, kv.v)
	}
	for _, kv := range s._bool {
		stmt.SetBool(kv.k, kv.v)
	}
	for _, kv := range s._float {
		stmt.SetFloat(kv.k, kv.v)
	}
	return stmt, nil
}

// Query sets the query string for the statement.
func (s *Stmt) Query(q string) {
	s.q = q
}

func (s *Stmt) String() string {
	values := []string{}
	for _, kv := range s.text {
		values = append(values, kv.String())
	}
	for _, kv := range s._int64 {
		values = append(values, kv.String())
	}
	for _, k := range s._null {
		values = append(values, fmt.Sprintf("{%s:null}", k))
	}
	for _, kv := range s._bytes {
		values = append(values, kv.String())
	}
	for _, kv := range s._bool {
		values = append(values, kv.String())
	}
	for _, kv := range s._float {
		values = append(values, kv.String())
	}
	return fmt.Sprintf("%q, [%s]", s.q, strings.Join(values, ","))
}

// SetText sets a text parameter on the statement.
func (s *Stmt) SetText(param string, value string) {
	s.text = append(s.text, kv[string]{param, value})
}

// SetInt64 sets an int64 parameter on the statement.
func (s *Stmt) SetInt64(param string, value int64) {
	s._int64 = append(s._int64, kv[int64]{param, value})
}

// SetNull sets a null parameter on the statement.
func (s *Stmt) SetNull(param string) {
	s._null = append(s._null, param)
}

// SetBytes sets a bytes parameter on the statement.
func (s *Stmt) SetBytes(param string, value []byte) {
	s._bytes = append(s._bytes, kv[[]byte]{param, value})
}

// SetBool sets a bool parameter on the statement.
func (s *Stmt) SetBool(param string, value bool) {
	s._bool = append(s._bool, kv[bool]{param, value})
}

// SetFloat sets a float parameter on the statement.
func (s *Stmt) SetFloat(param string, value float64) {
	s._float = append(s._float, kv[float64]{param, value})
}

// CaptureStmts is a helper for capturing statements. It is used in tests only.
type CaptureStmts struct {
	stmts  []Stmt
	insert []Stmt
}

func (c *CaptureStmts) Capture(stmt Stmt) {
	if c == nil {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(strings.ToLower(stmt.q)), "insert") {
		c.insert = append(c.insert, stmt)
		return
	}
	c.stmts = append(c.stmts, stmt)
}

// Len returns the number of captured statements. This does not include inserts.
func (c *CaptureStmts) Len() int {
	if c == nil {
		return 0
	}
	return len(c.stmts)
}

// Inserts returns the captured insert statements.
func (c *CaptureStmts) Inserts() []Stmt {
	if c == nil {
		return nil
	}
	return c.insert
}

// Stmt returns the captured statement at index i. This will not include inserts.
func (c *CaptureStmts) Stmt(i int) Stmt {
	if c == nil {
		return Stmt{}
	}
	return c.stmts[i]
}
