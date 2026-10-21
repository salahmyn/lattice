// Package store keeps a workspace's authored records and its latest block
// graph, in files or in PostgreSQL, behind one interface (spec §6).
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Record is one authored record: a YAML body whose `id` names it.
type Record struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Version   string `json:"version"` // opaque; a write names the version it replaces, "" for a new record
	Body      []byte `json:"-"`
	Author    string `json:"author,omitempty"`     // the body's author field
	UpdatedAt string `json:"updated_at,omitempty"` // when the stored copy last changed
}

// StaleError refuses a write whose version is not the stored one. Current is
// the stored version, "" when the record does not exist.
type StaleError struct{ Kind, ID, Current string }

func (e *StaleError) Error() string {
	if e.Current == "" {
		return fmt.Sprintf("%s %s does not exist; write it with no version", e.Kind, e.ID)
	}
	return fmt.Sprintf("%s %s is at version %s; read it again and write with that version", e.Kind, e.ID, e.Current)
}

// ErrNotFound is returned by Get for a record that does not exist.
var ErrNotFound = errors.New("record not found")

// Graph is the block graph one refresh writes.
type Graph struct {
	Blocks  []schema.Block       `json:"blocks,omitempty"`
	Edges   []schema.BlockEdge   `json:"block_edges,omitempty"`
	Sources []schema.SourceStamp `json:"sources,omitempty"`
	Flows   []schema.Flow        `json:"flows,omitempty"`
}

// Filter narrows a search; empty fields match everything.
type Filter struct {
	Kind, System, Tier string
	Limit              int // default 50
}

// Direction says which edges a relations walk follows.
type Direction string

const (
	Out  Direction = "out"
	In   Direction = "in"
	Both Direction = "both"
)

// Store keeps records and the latest graph. Lists and query results are in a
// fixed order: records and blocks by id, edges by from, kind, to.
type Store interface {
	Put(ctx context.Context, r Record) (Record, error)
	Get(ctx context.Context, kind, id string) (Record, error)
	List(ctx context.Context, kind string) ([]Record, error)
	ReplaceGraph(ctx context.Context, g Graph) error
	Graph(ctx context.Context) (Graph, error)
	Search(ctx context.Context, text string, f Filter) ([]schema.Block, error)
	Relations(ctx context.Context, id string, dir Direction, kinds []string, depth int) ([]schema.BlockEdge, error)
	// Where names the store in messages, e.g. a file path or a database schema.
	Where() string
	Close() error
}

// Open opens the store a workspace declares.
func Open(ctx context.Context, ws *workspace.Workspace) (Store, error) {
	if ws.Storage.Backend != "postgres" {
		return OpenFiles(ws), nil
	}
	env := ws.Storage.Postgres.DSNEnv
	dsn := os.Getenv(env)
	if dsn == "" {
		return nil, fmt.Errorf("storage is postgres but %s is not set", env)
	}
	st, err := OpenPostgres(ctx, dsn, ws.Storage.Postgres.Schema)
	if err != nil {
		return nil, fmt.Errorf("postgres (%s): %w", env, err) // the DSN itself is never printed
	}
	return st, nil
}

var name = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// checkRecord guards a write: kind and id become file names and SQL values,
// and the body must name the same id.
func checkRecord(r Record) error {
	if !name.MatchString(r.Kind) || !name.MatchString(r.ID) || r.Kind == ".." || r.ID == ".." {
		return fmt.Errorf("record %q/%q: kind and id may use letters, digits, '.', '_' and '-'", r.Kind, r.ID)
	}
	if id := IDOf(r.Body); id != r.ID {
		return fmt.Errorf("record %s/%s: its body has id %q", r.Kind, r.ID, id)
	}
	return nil
}

// IDOf returns a YAML body's top-level id, or "".
func IDOf(body []byte) string {
	id, _ := idOf(body)
	return id
}

func idOf(body []byte) (string, error) {
	var v struct {
		ID string `yaml:"id"`
	}
	err := yaml.Unmarshal(body, &v)
	return v.ID, err
}

func authorOf(body []byte) string {
	var v struct {
		Author string `yaml:"author"`
	}
	_ = yaml.Unmarshal(body, &v)
	return v.Author
}
