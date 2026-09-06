package runtime

import "time"

// PoolObserver is told what the pool does with a model server: when one is
// loaded, how long that took, and when one leaves and why.
//
// It exists so that the reasons a request was slow — a cold model, a model
// evicted out from under the last request — can be counted without the caller
// reaching into the pool's internals. It is a bystander: the pool tells it
// what happened and carries on regardless, so an observer that has gone slow,
// or one that panics, cannot stall or fail a request. Callbacks are made off
// the pool's lock and off the calling goroutine, in the order the pool decided
// things but not necessarily in the order they arrive.
//
// A nil observer (the default) makes every report a no-op.
type PoolObserver interface {
	// LoadStarted is called when a model server process has been started and
	// the pool begins waiting for it to answer.
	LoadStarted(repoID string)
	// LoadFinished is called when that wait ends, with how long it took and
	// the error if the model never became ready.
	LoadFinished(repoID string, took time.Duration, err error)
	// EntryStopped is called when a model server leaves the pool, with the
	// reason it left.
	EntryStopped(repoID string, reason StopReason)
}

// StopReason says why a model server left the pool. The pool removes an entry
// by seven paths and only one of them is an eviction, so counting them as one
// would make the load and eviction figures disagree with what happened.
type StopReason string

const (
	// StopEvicted is the only removal that is an eviction: the model was taken
	// out to make room for another one to load.
	StopEvicted StopReason = "evicted"
	// StopIdle is a model reaped for going untouched for the idle timeout.
	StopIdle StopReason = "idle"
	// StopUnloaded is a model the operator unloaded from the control panel.
	StopUnloaded StopReason = "unloaded"
	// StopAbandoned is a load whose last waiter gave up before it finished, so
	// there was nobody left to load it for.
	StopAbandoned StopReason = "abandoned"
	// StopLoadFailed is a model server that started but never became ready.
	StopLoadFailed StopReason = "load_failed"
	// StopCrashed is a model server that exited on its own after it had been
	// serving.
	StopCrashed StopReason = "crashed"
	// StopShutdown is every model server at the moment the pool closes.
	StopShutdown StopReason = "shutdown"
)

// notify hands one report to the observer, if there is one.
//
// It runs on its own goroutine, which is what keeps the promise in
// PoolObserver's own doc comment: this is called from inside p.mu in several
// places, and every one of them is on the path of an ordinary request. The
// recover is not defensive tidiness — without it a panic in a bystander's
// callback ends the whole server, which is a far worse outcome than a report
// nobody hears. There is no logger here to say so; the pool has none.
func (p *Pool) notify(fn func(PoolObserver)) {
	obs := p.opts.Observer
	if obs == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		fn(obs)
	}()
}
