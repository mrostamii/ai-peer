package node

import "time"

const latencyEMAAlpha = 0.2

func (r *Runtime) markInferenceStarted() time.Time {
	r.inflightInference.Add(1)
	return time.Now()
}

func (r *Runtime) markInferenceFinished(started time.Time) {
	r.inflightInference.Add(-1)
	d := time.Since(started)
	if d < 0 {
		d = 0
	}
	ms := float64(d.Milliseconds())
	r.statsMu.Lock()
	if !r.hasLatencySample {
		r.latencyEMAms = ms
		r.hasLatencySample = true
		r.statsMu.Unlock()
		return
	}
	r.latencyEMAms = latencyEMAAlpha*ms + (1-latencyEMAAlpha)*r.latencyEMAms
	r.statsMu.Unlock()
}

func (r *Runtime) healthSnapshot() (float64, int64) {
	load := float64(r.inflightInference.Load())
	r.statsMu.RLock()
	lat := int64(0)
	if r.hasLatencySample {
		lat = int64(r.latencyEMAms + 0.5)
	}
	r.statsMu.RUnlock()
	return load, lat
}
