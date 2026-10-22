package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Postgres keeps records and the latest graph in one schema of a PostgreSQL
// database. The graph tables are replaced on each refresh, in one transaction.
type Postgres struct {
	c *pgx.Conn
	s string // schema, a checked identifier
}

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

const ddl = `
CREATE SCHEMA IF NOT EXISTS {s};
CREATE TABLE IF NOT EXISTS {s}.records (
	kind text NOT NULL, id text NOT NULL, version bigint NOT NULL, body text NOT NULL,
	author text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (kind, id));
CREATE TABLE IF NOT EXISTS {s}.blocks (
	ord int NOT NULL, id text PRIMARY KEY, kind text NOT NULL, system text NOT NULL, tier text NOT NULL,
	title text NOT NULL, description text NOT NULL, data jsonb NOT NULL,
	tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple',
		regexp_replace(lower(id || ' ' || title || ' ' || description), '[^[:alnum:]]+', ' ', 'g'))) STORED);
CREATE INDEX IF NOT EXISTS blocks_tsv ON {s}.blocks USING gin (tsv);
CREATE TABLE IF NOT EXISTS {s}.block_edges (
	ord int NOT NULL, from_id text NOT NULL, to_id text NOT NULL, kind text NOT NULL, data jsonb NOT NULL,
	PRIMARY KEY (from_id, to_id, kind));
CREATE INDEX IF NOT EXISTS block_edges_to ON {s}.block_edges (to_id);
CREATE TABLE IF NOT EXISTS {s}.sources (ord int NOT NULL, id text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS {s}.flows (ord int NOT NULL, id text PRIMARY KEY, data jsonb NOT NULL);
`

// OpenPostgres connects and creates the schema's tables when missing.
func OpenPostgres(ctx context.Context, dsn, sch string) (*Postgres, error) {
	if !schemaName.MatchString(sch) {
		return nil, fmt.Errorf("schema %q is not a lower-case SQL identifier", sch)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil { // the parse error can quote the whole string, password included
		return nil, errors.New("cannot parse the connection string")
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p := &Postgres{c: c, s: sch}
	if _, err := c.Exec(ctx, p.q(ddl)); err != nil {
		_ = c.Close(ctx)
		return nil, fmt.Errorf("create schema %s: %w", sch, err)
	}
	return p, nil
}

func (p *Postgres) q(sql string) string {
	return strings.ReplaceAll(sql, "{s}", pgx.Identifier{p.s}.Sanitize())
}
func (p *Postgres) Where() string { return "postgres schema " + p.s }
func (p *Postgres) Close() error  { return p.c.Close(context.Background()) }

const recordCols = `id, version::text, body, author, to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

func scanRecord(kind string, row pgx.Row) (Record, error) {
	r := Record{Kind: kind}
	var body string
	err := row.Scan(&r.ID, &r.Version, &body, &r.Author, &r.UpdatedAt)
	r.Body = []byte(body)
	return r, err
}

func (p *Postgres) Get(ctx context.Context, kind, id string) (Record, error) {
	r, err := scanRecord(kind, p.c.QueryRow(ctx, p.q(`SELECT `+recordCols+` FROM {s}.records WHERE kind = $1 AND id = $2`), kind, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return r, err
}

func (p *Postgres) List(ctx context.Context, kind string) ([]Record, error) {
	rows, err := p.c.Query(ctx, p.q(`SELECT `+recordCols+` FROM {s}.records WHERE kind = $1 ORDER BY id COLLATE "C"`), kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(kind, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) Put(ctx context.Context, r Record) (Record, error) {
	if err := checkRecord(r); err != nil {
		return Record{}, err
	}
	var n int64
	if r.Version == "" {
		tag, err := p.c.Exec(ctx, p.q(`INSERT INTO {s}.records (kind, id, version, body, author) VALUES ($1, $2, 1, $3, $4)
			ON CONFLICT DO NOTHING`), r.Kind, r.ID, string(r.Body), authorOf(r.Body))
		if err != nil {
			return Record{}, err
		}
		n = tag.RowsAffected()
	} else {
		tag, err := p.c.Exec(ctx, p.q(`UPDATE {s}.records SET version = version + 1, body = $3, author = $4, updated_at = now()
			WHERE kind = $1 AND id = $2 AND version::text = $5`), r.Kind, r.ID, string(r.Body), authorOf(r.Body), r.Version)
		if err != nil {
			return Record{}, err
		}
		n = tag.RowsAffected()
	}
	if n == 0 {
		cur, err := p.Get(ctx, r.Kind, r.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			return Record{}, &StaleError{Kind: r.Kind, ID: r.ID}
		case err != nil:
			return Record{}, err
		}
		return Record{}, &StaleError{Kind: r.Kind, ID: r.ID, Current: cur.Version}
	}
	return p.Get(ctx, r.Kind, r.ID)
}

func (p *Postgres) ReplaceGraph(ctx context.Context, g Graph) error {
	tx, err := p.c.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, t := range []string{"blocks", "block_edges", "sources", "flows"} {
		if _, err := tx.Exec(ctx, p.q("DELETE FROM {s}."+t)); err != nil {
			return err
		}
	}
	var blocks, edges, sources, flows [][]any
	for i, b := range g.Blocks {
		data, _ := json.Marshal(b)
		blocks = append(blocks, []any{i, b.ID, b.Kind, b.System, b.Tier, b.Title, b.Description, data})
	}
	for i, e := range g.Edges {
		data, _ := json.Marshal(e)
		edges = append(edges, []any{i, e.From, e.To, e.Kind, data})
	}
	for i, s := range g.Sources {
		data, _ := json.Marshal(s)
		sources = append(sources, []any{i, s.ID, data})
	}
	for i, f := range g.Flows {
		data, _ := json.Marshal(f)
		flows = append(flows, []any{i, f.ID, data})
	}
	for _, c := range []struct {
		table string
		cols  []string
		rows  [][]any
	}{
		{"blocks", []string{"ord", "id", "kind", "system", "tier", "title", "description", "data"}, blocks},
		{"block_edges", []string{"ord", "from_id", "to_id", "kind", "data"}, edges},
		{"sources", []string{"ord", "id", "data"}, sources},
		{"flows", []string{"ord", "id", "data"}, flows},
	} {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{p.s, c.table}, c.cols, pgx.CopyFromRows(c.rows)); err != nil {
			return fmt.Errorf("write %s: %w", c.table, err)
		}
	}
	return tx.Commit(ctx)
}

// loadJSON appends each row's decoded data column to out, in ord order.
func loadJSON[T any](ctx context.Context, p *Postgres, table string, out *[]T) error {
	rows, err := p.c.Query(ctx, p.q("SELECT data FROM {s}."+table+" ORDER BY ord"))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var v T
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		*out = append(*out, v)
	}
	return rows.Err()
}

func (p *Postgres) Graph(ctx context.Context) (Graph, error) {
	var g Graph
	for _, load := range []func() error{
		func() error { return loadJSON(ctx, p, "blocks", &g.Blocks) },
		func() error { return loadJSON(ctx, p, "block_edges", &g.Edges) },
		func() error { return loadJSON(ctx, p, "sources", &g.Sources) },
		func() error { return loadJSON(ctx, p, "flows", &g.Flows) },
	} {
		if err := load(); err != nil {
			return Graph{}, err
		}
	}
	return g, nil
}

func (p *Postgres) Search(ctx context.Context, text string, f Filter) ([]schema.Block, error) {
	terms := words(text)
	if len(terms) == 0 {
		return nil, nil
	}
	for i := range terms {
		terms[i] += ":*" // terms are letters and digits only, so the tsquery needs no escaping
	}
	var out []schema.Block
	rows, err := p.c.Query(ctx, p.q(`SELECT data FROM {s}.blocks WHERE tsv @@ to_tsquery('simple', $1)
		AND ($2 = '' OR kind = $2) AND ($3 = '' OR system = $3) AND ($4 = '' OR tier = $4)
		ORDER BY id COLLATE "C" LIMIT $5`), strings.Join(terms, " & "), f.Kind, f.System, f.Tier, limitOf(f))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var b schema.Block
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Relations walks the edges with a recursive CTE; the semantics are relations()'s.
func (p *Postgres) Relations(ctx context.Context, id string, dir Direction, kinds []string, depth int) ([]schema.BlockEdge, error) {
	if kinds == nil {
		kinds = []string{}
	}
	rows, err := p.c.Query(ctx, p.q(`
WITH RECURSIVE walk(node, depth) AS (
	SELECT $1::text, 0
	UNION
	SELECT CASE WHEN e.from_id = w.node AND $2 <> 'in' THEN e.to_id ELSE e.from_id END, w.depth + 1
	FROM walk w JOIN {s}.block_edges e
		ON ($2 <> 'in' AND e.from_id = w.node) OR ($2 <> 'out' AND e.to_id = w.node)
	WHERE w.depth + 1 < $3 AND (cardinality($4::text[]) = 0 OR e.kind = ANY($4))
)
SELECT DISTINCT e.data FROM {s}.block_edges e JOIN walk w
	ON ($2 <> 'in' AND e.from_id = w.node) OR ($2 <> 'out' AND e.to_id = w.node)
WHERE cardinality($4::text[]) = 0 OR e.kind = ANY($4)`), id, string(dir), clampDepth(depth), kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schema.BlockEdge
	for rows.Next() {
		var data []byte
		var e schema.BlockEdge
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sortEdges(out)
	return out, rows.Err()
}
