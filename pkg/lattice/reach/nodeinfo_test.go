package reach_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
)

// NodeInfo names the file and language of a symbol and of a pseudo-node (a
// table, a module's top-level code), and says which is a symbol.
func TestNodeInfo(t *testing.T) {
	mods := parse(t, postgres.New(nil, nil), map[string]string{
		"db/schema.sql": "create policy owner_reads on public.accounts for select using (true);\n",
	})
	mods = append(mods, parse(t, typescript.New(), map[string]string{
		"app/api.ts": "export async function load(client: any) {\n  await client.from('accounts').select('*');\n}\n",
	})...)
	g := reach.Build(mods, reach.Options{})
	for fqn, want := range map[string]reach.NodeInfo{
		"app.api.load":                           {FQN: "app.api.load", Kind: "function", File: "app/api.ts", Language: "typescript", Symbol: true},
		"sql.table.public.accounts":              {FQN: "sql.table.public.accounts", Kind: "table", File: "db/schema.sql", Language: "postgres"},
		"sql.policy.public.accounts.owner_reads": {FQN: "sql.policy.public.accounts.owner_reads", Kind: "function", File: "db/schema.sql", Language: "postgres", Symbol: true},
	} {
		got, ok := g.NodeInfo(fqn)
		if !ok || got != want {
			t.Errorf("%s = %+v (%v), want %+v", fqn, got, ok, want)
		}
	}
	if _, ok := g.NodeInfo("app.api.ghost"); ok {
		t.Error("a name that is no node has no info")
	}
}
