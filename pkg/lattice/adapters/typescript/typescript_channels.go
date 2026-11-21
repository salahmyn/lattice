package typescript

// @module-feature adapters.typescript

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// SetChannelWrappers declares the project's own wrappers around a queue or
// event client (workspace.yaml channels:). Call it before parsing; a call of a
// wrapper then records a publish or consume ref (see wrapperRef).
func (a *Adapter) SetChannelWrappers(w []ir.ChannelWrapper) {
	a.wrappers = append([]ir.ChannelWrapper(nil), w...)
}

// pgmqPublishRPC and pgmqConsumeRPC are the database functions that send to
// and read from a pgmq queue. The pgmq_ forms are the public wrappers a
// project defines; the bare ones are exposed in the pgmq_public schema.
var (
	pgmqPublishRPC = map[string]bool{"pgmq_send": true, "pgmq_send_batch": true}
	pgmqConsumeRPC = map[string]bool{"pgmq_read": true, "pgmq_pop": true, "pgmq_read_with_poll": true}
	pgmqPublicSend = map[string]bool{"send": true, "send_batch": true}
	pgmqPublicRead = map[string]bool{"read": true, "pop": true, "read_with_poll": true}
)

// bullmqModules are the packages whose Queue and Worker are read.
var bullmqModules = map[string]bool{"bullmq": true, "bull": true}

// queueMethods are the BullMQ queue methods that enqueue a job.
var queueMethods = map[string]bool{"add": true, "addBulk": true}

// channelState is what the reference walker knows of a module's queue
// clients, read once before the walk.
type channelState struct {
	queueCtors  map[string]bool   // local names that construct a BullMQ queue
	workerCtors map[string]bool   // local names that construct a BullMQ worker
	namespaces  map[string]bool   // local names of the whole bullmq module
	queues      map[string]string // variable -> channel key ("" when its name is computed)
}

func (s *channelState) empty() bool {
	return len(s.queueCtors) == 0 && len(s.workerCtors) == 0 && len(s.namespaces) == 0
}

// prepareChannels reads the module's BullMQ imports and the variables bound
// to a queue: const q = new Queue('emails'). The binding analysis stays inside
// the module and follows only a plain identifier bound by a variable
// declaration (const, let or var, at any depth); a queue held in a property
// (this.queue), a parameter or another module's export is not followed, and
// a name declared twice for different queues is treated as computed.
func (w *tsRefWalker) prepareChannels(root *sitter.Node) {
	st := &channelState{queueCtors: map[string]bool{}, workerCtors: map[string]bool{},
		namespaces: map[string]bool{}, queues: map[string]string{}}
	w.ch = st
	for _, im := range w.p.mod.Imports {
		if !bullmqModules[im.From] || im.Name == "" || im.Dynamic {
			continue
		}
		switch {
		case im.Member == "Queue":
			st.queueCtors[im.Name] = true
		case im.Member == "Worker" && im.From == "bullmq":
			st.workerCtors[im.Name] = true
		case im.Member == "" || im.Member == "default":
			if im.From == "bull" {
				st.queueCtors[im.Name] = true // bull's default export constructs a queue
			}
			st.namespaces[im.Name] = true
		}
	}
	if st.empty() {
		return
	}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "variable_declarator" {
			name, val := n.ChildByFieldName("name"), n.ChildByFieldName("value")
			if name != nil && name.Kind() == "identifier" && val != nil && w.isQueueCtor(val) {
				key := w.constructedChannel(val, ir.ChannelQueue)
				id := w.text(name)
				if prev, seen := st.queues[id]; seen && prev != key {
					key = "" // two different queues under one name: not told apart
				}
				st.queues[id] = key
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(uint(i)))
		}
	}
	walk(root)
}

// ctorOf returns the constructor of a new expression written as an identifier
// or as ns.Name through a namespace import of bullmq, and the bare name.
func (w *tsRefWalker) ctorOf(n *sitter.Node) (name string, viaNS, ok bool) {
	if n == nil || n.Kind() != "new_expression" {
		return "", false, false
	}
	c := n.ChildByFieldName("constructor")
	if c == nil {
		return "", false, false
	}
	switch c.Kind() {
	case "identifier":
		return w.text(c), false, true
	case "member_expression":
		obj, prop := c.ChildByFieldName("object"), c.ChildByFieldName("property")
		if obj != nil && prop != nil && obj.Kind() == "identifier" && w.ch.namespaces[w.text(obj)] {
			return w.text(prop), true, true
		}
	}
	return "", false, false
}

// isQueueCtor reports whether n is new Queue(...) of BullMQ.
func (w *tsRefWalker) isQueueCtor(n *sitter.Node) bool {
	name, viaNS, ok := w.ctorOf(n)
	if !ok {
		return false
	}
	if viaNS {
		return name == "Queue"
	}
	return w.ch.queueCtors[name]
}

// isWorkerCtor reports whether n is new Worker(...) of BullMQ.
func (w *tsRefWalker) isWorkerCtor(n *sitter.Node) bool {
	name, viaNS, ok := w.ctorOf(n)
	if !ok {
		return false
	}
	if viaNS {
		return name == "Worker"
	}
	return w.ch.workerCtors[name]
}

// constructedChannel is the key of the channel a constructor call names with
// its first argument: "kind:name", or "" when it is not a literal.
func (w *tsRefWalker) constructedChannel(ctor *sitter.Node, kind string) string {
	args := ctor.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	return w.keyOf(kind, args.NamedChild(0))
}

// keyOf is the channel key a literal argument gives, "" when it is computed.
func (w *tsRefWalker) keyOf(kind string, arg *sitter.Node) string {
	if arg == nil {
		return ""
	}
	if lit, ok := w.literalURL(arg); ok {
		return ir.ChannelKey(kind, lit)
	}
	return ""
}

// channelCall records the channel ends of a call:
//
//   - queue.add(...) and queue.addBulk(...), where queue is bound in this
//     module to new Queue('name') of bullmq or bull, or new Queue('name').add(...);
//   - supabase.rpc('pgmq_send', { queue_name: 'q' }) (and pgmq_send_batch)
//     publish, pgmq_read, pgmq_pop and pgmq_read_with_poll consume; the bare
//     send, read and pop of the pgmq_public schema too;
//   - a call of a wrapper declared in workspace.yaml channels:.
//
// A channel name that is not a literal gives a ref with no name, which the
// core counts as dynamic.
func (w *tsRefWalker) channelCall(fn, args *sitter.Node) {
	if fn == nil || args == nil {
		return
	}
	if fn.Kind() == "member_expression" {
		obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
		if obj != nil && prop != nil {
			switch name := w.text(prop); {
			case queueMethods[name]:
				w.queueAdd(obj, prop)
			case name == "rpc":
				w.pgmqRPC(obj, prop, args)
			}
		}
	}
	w.wrapperRef(fn, args)
}

// queueAdd records queue.add(...) when its receiver is a queue.
func (w *tsRefWalker) queueAdd(obj, at *sitter.Node) {
	if w.ch == nil || w.ch.empty() {
		return
	}
	switch {
	case obj.Kind() == "identifier":
		if key, ok := w.ch.queues[w.text(obj)]; ok {
			w.emit(at, ir.Ref{Name: key, Kind: ir.RefPublish})
		}
	case w.isQueueCtor(obj):
		var lit *sitter.Node
		if args := obj.ChildByFieldName("arguments"); args != nil && args.NamedChildCount() > 0 {
			lit = args.NamedChild(0)
		}
		w.emit(at, withLit(ir.Ref{Name: w.constructedChannel(obj, ir.ChannelQueue), Kind: ir.RefPublish}, lit))
	}
}

// pgmqRPC records supabase.rpc('pgmq_send', { queue_name: 'q' }) and the
// functions that read a queue.
func (w *tsRefWalker) pgmqRPC(obj, at, args *sitter.Node) {
	if args.NamedChildCount() == 0 || args.NamedChild(0).Kind() != "string" {
		return
	}
	fn := w.p.literalText(args.NamedChild(0))
	public := w.schemaOf(obj) == "pgmq_public"
	var kind string
	switch {
	case pgmqPublishRPC[fn], public && pgmqPublicSend[fn]:
		kind = ir.RefPublish
	case pgmqConsumeRPC[fn], public && pgmqPublicRead[fn]:
		kind = ir.RefConsume
	default:
		return
	}
	key := ""
	var lit *sitter.Node
	if args.NamedChildCount() > 1 && args.NamedChild(1).Kind() == "object" {
		if val, state := w.property(args.NamedChild(1), "queue_name"); state == propFound {
			key = w.keyOf(ir.ChannelQueue, val)
			if kind == ir.RefPublish {
				lit = val
			}
		}
	}
	w.emit(at, withLit(ir.Ref{Name: key, Kind: kind}, lit))
}

// workerRef records new Worker('name', handler) of bullmq: the handler is the
// consumer when it is a named function, else the code that holds the call.
func (w *tsRefWalker) workerRef(n *sitter.Node) {
	if w.ch == nil || w.ch.empty() || !w.isWorkerCtor(n) {
		return
	}
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return
	}
	r := ir.Ref{Name: w.keyOf(ir.ChannelQueue, args.NamedChild(0)), Kind: ir.RefConsume}
	if args.NamedChildCount() > 1 {
		if h := args.NamedChild(1); h.Kind() == "identifier" {
			r.Handler = w.text(h)
		}
	}
	w.emit(n.ChildByFieldName("constructor"), r)
}

// calleeChain is the callee as written: a chain of identifiers, with this
// where it starts the chain. nil for any other callee.
func (w *tsRefWalker) calleeChain(fn *sitter.Node) []string {
	switch fn.Kind() {
	case "identifier":
		return []string{w.text(fn)}
	case "this":
		return []string{"this"}
	case "member_expression":
		obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
		if obj == nil || prop == nil {
			return nil
		}
		head := w.calleeChain(obj)
		if head == nil {
			return nil
		}
		return append(head, w.text(prop))
	}
	return nil
}

// wrapperRef records a call of a declared wrapper. The declared call matches
// the callee by its trailing segments: "jobs.enqueue" matches jobs.enqueue()
// and app.jobs.enqueue(), a bare "enqueue" any callee that ends in enqueue.
func (w *tsRefWalker) wrapperRef(fn, args *sitter.Node) {
	if len(w.p.wrappers) == 0 {
		return
	}
	chain := w.calleeChain(fn)
	if chain == nil {
		return
	}
	at := fn
	if fn.Kind() == "member_expression" {
		at = fn.ChildByFieldName("property")
	}
	for _, wr := range w.p.wrappers {
		if !endsWith(chain, strings.Split(wr.Call, ".")) {
			continue
		}
		var arg *sitter.Node
		if wr.Arg < int(args.NamedChildCount()) {
			arg = args.NamedChild(uint(wr.Arg))
		}
		r := ir.Ref{Name: w.keyOf(wr.Kind, arg), Kind: ir.RefPublish}
		if !wr.Consume {
			r = withLit(r, arg)
		}
		if wr.Consume {
			r.Kind = ir.RefConsume
			if wr.HandlerArg >= 0 && wr.HandlerArg < int(args.NamedChildCount()) {
				if h := args.NamedChild(uint(wr.HandlerArg)); h.Kind() == "identifier" {
					r.Handler = w.text(h)
				}
			}
		}
		w.emit(at, r)
	}
}

// endsWith reports whether the last len(suffix) elements of chain equal suffix.
func endsWith(chain, suffix []string) bool {
	if len(suffix) == 0 || len(suffix) > len(chain) {
		return false
	}
	off := len(chain) - len(suffix)
	for i, s := range suffix {
		if chain[off+i] != s {
			return false
		}
	}
	return true
}
