package schema

// @module-feature schema

import (
	"sort"

	"github.com/bmatcuk/doublestar/v4"
)

// Layers are the runtime kinds of code, a closed vocabulary. A layer says what
// kind of runtime a symbol or block lives in; it is not a system (who owns and
// deploys it), not a block tier (the frontend or backend grouping of a source)
// and not a criterion tier (how critical a success criterion is).
const (
	LayerUI       = "ui"       // code that runs in a browser or an app
	LayerAPI      = "api"      // request handlers: routes and RPC endpoints
	LayerService  = "service"  // application logic and command-line programs
	LayerData     = "data"     // database functions, policies and triggers
	LayerAsync    = "async"    // scheduled jobs, queue workers, event and webhook consumers
	LayerEdge     = "edge"     // edge or serverless functions
	LayerExternal = "external" // third-party integrations
	LayerUnknown  = "unknown"  // no adapter fact and no declared rule applies
)

// Layers lists the vocabulary in a fixed order.
var Layers = []string{
	LayerUI, LayerAPI, LayerService, LayerData, LayerAsync, LayerEdge, LayerExternal, LayerUnknown,
}

// ValidLayer reports whether s is one of the vocabulary.
func ValidLayer(s string) bool {
	for _, l := range Layers {
		if l == s {
			return true
		}
	}
	return false
}

// Where a layer came from (GraphSymbol.LayerSource). Unknown has no source.
const (
	LayerSourceAdapter = "adapter" // a fact the extractor read: the symbol's language, or its entry point
	LayerSourceRule    = "rule"    // a human-declared path rule in workspace.yaml
)

// LayerRule is one entry of the `layers:` list in workspace.yaml: files whose
// path matches Glob (doublestar syntax, relative to the code root) are in
// Layer. The first matching rule wins.
type LayerRule struct {
	Glob  string `yaml:"glob" json:"glob"`
	Layer string `yaml:"layer" json:"layer"`
}

// EntryPointLayer is the layer of the handler of an entry point of the given
// kind, and false for a kind with no mapping.
func EntryPointLayer(kind string) (string, bool) {
	switch kind {
	case EntryPointKindHTTP, EntryPointKindGRPC:
		return LayerAPI, true
	case EntryPointKindCron, EntryPointKindQueue, EntryPointKindEventConsumer, EntryPointKindWebhook:
		return LayerAsync, true
	case EntryPointKindCLI:
		return LayerService, true
	}
	return "", false
}

// LayerSet returns the sorted, de-duplicated non-empty layers of ls.
func LayerSet(ls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range ls {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// LayerOfFile is the layer a file's path gets from rules: the first rule whose
// glob matches decides, and a rule that is not valid is skipped. A file no
// rule matches, or one the matching rule gives the unknown layer, reads
// LayerUnknown.
func LayerOfFile(rules []LayerRule, file string) string {
	for _, r := range rules {
		if !ValidLayer(r.Layer) || r.Glob == "" || !doublestar.ValidatePattern(r.Glob) {
			continue
		}
		if ok, _ := doublestar.Match(r.Glob, file); ok {
			return r.Layer
		}
	}
	return LayerUnknown
}
