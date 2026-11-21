package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// confirm.routes reads as a list or one name; empty accepts both routes; a
// list accepts only what it names, and a value naming no route accepts
// nothing and is reported by Check.
// @verifies confirm:INV-22
func TestConfirmRoutes(t *testing.T) {
	dir := t.TempDir()
	load := func(body string) Confirm {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := LoadConfirm(dir)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c, err := LoadConfirm(t.TempDir()); err != nil || c.Narrowed() || !c.Accepts(RouteReview) || !c.Accepts(RouteSignature) {
		t.Errorf("no config.yaml accepts both: %+v %v", c, err)
	}
	if c := load("confirm:\n  routes: [signature]\n"); !reflect.DeepEqual(c.Routes, Routes{"signature"}) || c.Accepts(RouteReview) ||
		!c.Accepts(RouteSignature) || !c.Narrowed() || c.Accepted() != "signature" || c.Check() != nil {
		t.Errorf("[signature]: %+v", c)
	}
	if c := load("confirm:\n  routes: review\n"); !reflect.DeepEqual(c.Routes, Routes{"review"}) || c.Accepts(RouteSignature) {
		t.Errorf("a scalar: %+v", c)
	}
	if c := load("confirm:\n  routes: [Review, signature]\n"); c.Narrowed() || c.Check() != nil {
		t.Errorf("both, in any case: %+v", c)
	}
	c := load("confirm:\n  routes: [sig]\n")
	if c.Accepts(RouteSignature) || c.Accepts(RouteReview) || c.Accepted() != "none" || c.Check() == nil {
		t.Errorf("an unknown value accepts nothing and is reported: %+v %v", c, c.Check())
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte("confirm:\n  routes: {a: b}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfirm(dir); err == nil {
		t.Error("a mapping is not a list of routes")
	}
}
