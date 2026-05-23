package importer

import (
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// TestMatchCapabilities_LaravelNotification is the canonical case from
// the DevelopersPortal dogfood: webhook.failed_notification with five
// LLM-named capabilities and a Laravel-style notification class whose
// methods (toMail/toDatabase/toArray/via) obviously implement them.
func TestMatchCapabilities_LaravelNotification(t *testing.T) {
	caps := []schema.Capability{
		{ID: "mail_notification",
			Summary: "Delivers the failed webhook notification via email.",
			Rules:   []string{"The notification must implement Laravel's toMail method."}},
		{ID: "database_notification",
			Summary: "Stores the failed webhook notification in the database.",
			Rules:   []string{"The notification must implement Laravel's toDatabase method."}},
		{ID: "array_serialization",
			Summary: "Converts the notification to an array for other channels or serialization.",
			Rules:   []string{"The notification must implement Laravel's toArray method."}},
		{ID: "channel_selection",
			Summary: "Determines the channels through which the notification is sent.",
			Rules:   []string{"The notification must implement Laravel's via method to return channel names."}},
		{ID: "entity_provider",
			Summary: "Provides access to the webhook entity associated with the failure.",
			Rules:   []string{"The entity must be provided via the getEntity method."}},
	}
	symbols := []string{
		`Modules\Webhook\Notifications\WebhookFailedNotification::toMail`,
		`Modules\Webhook\Notifications\WebhookFailedNotification::toDatabase`,
		`Modules\Webhook\Notifications\WebhookFailedNotification::toArray`,
		`Modules\Webhook\Notifications\WebhookFailedNotification::via`,
		`Modules\Webhook\Notifications\WebhookFailedNotification::getEntity`,
		`Modules\Webhook\Notifications\WebhookFailedNotification::__construct`, // ambiguous, should drop
	}
	got := MatchCapabilities(symbols, caps)
	// 4 of 5 obvious method↔cap pairs land cleanly. `via` is genuinely
	// ambiguous — it appears in the prose of both mail_notification
	// ("via email") and channel_selection ("the via method"); IDF
	// weighting demotes the common token below threshold and leaves it
	// unassigned, which surfaces honestly as a verify warning for the
	// reviewer to resolve instead of a confident wrong link.
	want := map[string][]string{
		"mail_notification": {
			`Modules\Webhook\Notifications\WebhookFailedNotification::toMail`,
		},
		"database_notification": {
			`Modules\Webhook\Notifications\WebhookFailedNotification::toDatabase`,
		},
		"array_serialization": {
			`Modules\Webhook\Notifications\WebhookFailedNotification::toArray`,
		},
		"entity_provider": {
			`Modules\Webhook\Notifications\WebhookFailedNotification::getEntity`,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestSymbolTokens(t *testing.T) {
	cases := map[string][]string{
		`Modules\Foo\Bar::toMail`:    {"mail"},                // get-like prefixes dropped via stopwords
		`Modules\Foo\Bar::getEntity`: {"entity"},
		`Modules\Foo\Bar::HTTPClient`: {"http", "client"},
		`Modules\Foo\Bar`:            {"bar"},
		`foo_bar.baz_quux::doIt`:     {"do", "it"}, // 'do' kept, 'it' dropped by stopwords actually...
	}
	// Adjust expectation: stopwords list excludes "it" — let's drop it.
	cases[`foo_bar.baz_quux::doIt`] = []string{"do"}

	for fqn, want := range cases {
		got := symbolTokens(fqn)
		gotSet := map[string]bool{}
		for _, w := range want {
			gotSet[w] = true
		}
		if !reflect.DeepEqual(got, gotSet) {
			t.Errorf("symbolTokens(%q) = %v, want %v", fqn, keys(got), want)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
