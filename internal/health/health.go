package health

import "sync/atomic"

// Readiness is shared by the lifecycle controller and the health handler.
type Readiness struct {
	ready atomic.Bool
}

func (r *Readiness) Set(ready bool) { r.ready.Store(ready) }
func (r *Readiness) IsReady() bool  { return r.ready.Load() }
