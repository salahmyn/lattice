package schema

// EntryPoint is one trigger of the running system — an HTTP route, a CLI
// command, a scheduled job, a queue worker, or an event consumer — paired
// with its handler symbol and the features that handler reaches through
// the call graph. Together they form the second axis of the knowledge
// graph: features describe meaning, entry points describe invocation.
type EntryPoint struct {
	ID      string       `yaml:"id" json:"id"`
	Version int          `yaml:"version" json:"version"`
	Status  Status       `yaml:"status" json:"status"` // proposal | production | deprecated
	Kind    string       `yaml:"kind" json:"kind"`     // see EntryPointKind* constants
	Purpose string       `yaml:"purpose,omitempty" json:"purpose,omitempty"`
	Owners  Owners       `yaml:"owners,omitempty" json:"owners,omitempty"`
	Trigger Trigger      `yaml:"trigger" json:"trigger"`
	Handler Handler      `yaml:"handler" json:"handler"`
	Flow    []FlowStep   `yaml:"flow,omitempty" json:"flow,omitempty"`
	// SideEffects is what this entry point causes the system to do
	// (writes, network calls, queue publishes). Populated by the flow
	// tracer.
	SideEffects []SideEffect `yaml:"side_effects,omitempty" json:"side_effects,omitempty"`
	// InvariantsTouched is derived: every invariant declared by any
	// feature on the flow.
	InvariantsTouched []string `yaml:"invariants_touched,omitempty" json:"invariants_touched,omitempty"`
}

// Recognised entry-point kinds. New kinds need a detector AND a view-
// rendering case in pkg/lattice/views.
const (
	EntryPointKindHTTP          = "http_route"
	EntryPointKindCLI           = "cli"
	EntryPointKindCron          = "cron"
	EntryPointKindQueue         = "queue"
	EntryPointKindWebhook       = "webhook"
	EntryPointKindEventConsumer = "event_consumer"
	EntryPointKindGRPC          = "grpc"
)

// Trigger is the kind-specific descriptor of how the entry point fires.
// Only the subset of fields relevant to the EntryPoint.Kind is populated.
// A flat struct (rather than a tagged union) keeps the YAML readable and
// the Go API straightforward.
type Trigger struct {
	// HTTP route fields.
	Method     string   `yaml:"method,omitempty" json:"method,omitempty"`
	Path       string   `yaml:"path,omitempty" json:"path,omitempty"`
	Middleware []string `yaml:"middleware,omitempty" json:"middleware,omitempty"`
	// Scheduled / cron field.
	Schedule string `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	// Queue / event_consumer fields.
	Queue string `yaml:"queue,omitempty" json:"queue,omitempty"`
	Event string `yaml:"event,omitempty" json:"event,omitempty"`
	// CLI field.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`
}

// Handler is the first symbol that runs when the trigger fires. The flow
// tracer walks the call graph outward from here.
type Handler struct {
	Symbol string `yaml:"symbol" json:"symbol"`
	File   string `yaml:"file,omitempty" json:"file,omitempty"`
	Line   int    `yaml:"line,omitempty" json:"line,omitempty"`
}

// FlowStep is one feature reached on the path from the handler. The flow
// tracer emits one per distinct feature it visits, with capability link
// when discernible.
type FlowStep struct {
	Feature    string   `yaml:"feature" json:"feature"`
	Capability string   `yaml:"capability,omitempty" json:"capability,omitempty"`
	// ViaSymbols is the chain of non-annotated symbols traversed between
	// the previous step (or the handler) and this feature — debugging
	// breadcrumb, not for end-user views.
	ViaSymbols []string `yaml:"via_symbols,omitempty" json:"via_symbols,omitempty"`
}

// SideEffect is something the entry point causes the system to do. The
// flow tracer recognises common patterns (Eloquent persistence, Guzzle
// HTTP, dispatch()) and emits one of these per touched site.
type SideEffect struct {
	Kind   string `yaml:"kind" json:"kind"`     // db_write | http_call | queue_publish | event_emit | file_write
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
}
