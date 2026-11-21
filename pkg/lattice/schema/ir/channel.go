package ir

// @module-feature reachability

import "strings"

// Channel kinds: the vocabulary a channel key starts with.
const (
	ChannelQueue  = "queue"  // a job queue: pgmq, BullMQ, a declared wrapper
	ChannelEvent  = "event"  // a named event, consumed by an event_consumer entry point
	ChannelNotify = "notify" // a Postgres NOTIFY channel
	ChannelTopic  = "topic"  // a pub/sub topic, through a declared wrapper
)

// ChannelKinds lists the vocabulary in a fixed order.
var ChannelKinds = []string{ChannelQueue, ChannelEvent, ChannelNotify, ChannelTopic}

// ValidChannelKind reports whether k is one of ChannelKinds.
func ValidChannelKind(k string) bool {
	for _, c := range ChannelKinds {
		if c == k {
			return true
		}
	}
	return false
}

// ChannelKey is the key of a channel end, "<kind>:<name>". It is "" when the
// name is not a literal (empty), which the core counts as dynamic.
func ChannelKey(kind, name string) string {
	if kind == "" || name == "" {
		return ""
	}
	return kind + ":" + name
}

// SplitChannelKey splits a key made by ChannelKey. ok is false for a key with
// no kind or no name.
func SplitChannelKey(key string) (kind, name string, ok bool) {
	kind, name, ok = strings.Cut(key, ":")
	return kind, name, ok && kind != "" && name != ""
}

// ChannelWrapper is one declared wrapper around a queue or event client
// (workspace.yaml channels:), handed to an adapter that reads call arguments.
type ChannelWrapper struct {
	// Call is the callee as written, compared by its trailing segments:
	// "jobs.enqueue" matches jobs.enqueue(...) and app.jobs.enqueue(...), a
	// bare "enqueue" matches any callee whose last segment is enqueue.
	Call string
	// Arg is the zero-based argument that names the channel.
	Arg int
	// Kind is one of ChannelKinds.
	Kind string
	// Consume is true for a consuming end, false for a publishing one.
	Consume bool
	// HandlerArg is the zero-based argument that is the handler, or -1.
	HandlerArg int
}

// ChannelConfigurable is implemented by an adapter that reads declared
// wrappers. Extraction calls it once, before any file is parsed.
type ChannelConfigurable interface {
	SetChannelWrappers(w []ChannelWrapper)
}

// HTTPOriginConfigurable is implemented by an adapter that reads the declared
// origins (workspace.yaml http.origins): it takes every entry and keeps the
// env:<NAME> and setting:<name> forms it can read in source. Extraction calls
// it once, before any file is parsed.
type HTTPOriginConfigurable interface {
	SetHTTPOrigins(origins []string)
}

// HTTPSecretConfigurable is implemented by an adapter that reads the declared
// secrets (workspace.yaml http.secrets): a secret name mapped to the route
// path the URL it holds points at. Extraction calls it once, before any file
// is parsed.
type HTTPSecretConfigurable interface {
	SetHTTPSecrets(secrets map[string]string)
}
