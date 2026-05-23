package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestPromoteParentsCreatesMissingAncestors mirrors the v0.2.0 dogfood
// scenario: deeply-dotted LLM ids land on disk and verify cascades with
// SUBFEATURE_PARENT_MISSING for every ancestor.
func TestPromoteParentsCreatesMissingAncestors(t *testing.T) {
	dir := t.TempDir()
	// Three leaves and one already-declared mid-level ancestor.
	write(t, dir, "accounts/api/wrappers/subscription.yaml", "id: accounts.api.wrappers.subscription\n")
	write(t, dir, "accounts/http/requests.yaml", "id: accounts.http.requests\n")
	write(t, dir, "accounts/api/wrappers.yaml", "id: accounts.api.wrappers\n") // already exists
	write(t, dir, "webhook/failed_notification.yaml", "id: webhook.failed_notification\n")

	created, err := PromoteParents(dir)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	sort.Strings(created)
	want := []string{"accounts", "webhook", "accounts.api", "accounts.http"}
	sort.Strings(want)
	if !reflect.DeepEqual(created, want) {
		t.Errorf("created = %v, want %v", created, want)
	}

	// Idempotent: second run is a no-op.
	again, err := PromoteParents(dir)
	if err != nil || len(again) != 0 {
		t.Errorf("idempotent re-run created %v err=%v", again, err)
	}

	// Created files validate to the expected ids.
	for _, fid := range created {
		p := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(fid, ".", "/"))+".yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("created file unreadable: %v", err)
		}
		if !idLineRE.Match(data) {
			t.Errorf("created %s missing id line:\n%s", p, data)
		}
	}
}

// TestMissingAncestorsHandlesMultiLevelGaps covers the case where two
// adjacent levels are both missing — the v0.2.0 cascade went up to 4 deep.
func TestMissingAncestorsHandlesMultiLevelGaps(t *testing.T) {
	got := missingAncestors(map[string]bool{
		"core.traits.locker.run_once": true,
	})
	want := []string{"core", "core.traits", "core.traits.locker"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
