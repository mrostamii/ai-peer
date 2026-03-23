package node

import "time"

const latencyEMAAlpha = 0.2

func (r *Runtime) markInferenceStarted() time.Time {
	r.inflightInference.Add(1)
	return time.Now()
}

func (r *Runtime) markInferenceFinished() {
	r.inflightInference.Add(-1)
}

func (r *Runtime) recordInferenceSample(totalDuration, ttft time.Duration, completionTokens int64) {
	d := totalDuration
	if d < 0 {
		d = 0
	}
	ms := float64(d.Milliseconds())
	ttftMS := float64(0)
	if ttft > 0 {
		ttftMS = float64(ttft.Milliseconds())
	}
	decodeTPS := float64(0)
	if completionTokens > 0 && d > 0 {
		decodeTPS = float64(completionTokens) / d.Seconds()
	}

	r.statsMu.Lock()
	if !r.hasLatencySample {
		r.latencyEMAms = ms
		r.ttftEMAms = ttftMS
		r.hasTTFTSample = ttft > 0
		r.decodeTPSEMA = decodeTPS
		r.hasDecodeSample = decodeTPS > 0
		r.hasLatencySample = true
		r.statsMu.Unlock()
		return
	}
	r.latencyEMAms = latencyEMAAlpha*ms + (1-latencyEMAAlpha)*r.latencyEMAms
	if ttft > 0 {
		if !r.hasTTFTSample {
			r.ttftEMAms = ttftMS
			r.hasTTFTSample = true
		} else {
			r.ttftEMAms = latencyEMAAlpha*ttftMS + (1-latencyEMAAlpha)*r.ttftEMAms
		}
	}
	if decodeTPS > 0 {
		if !r.hasDecodeSample {
			r.decodeTPSEMA = decodeTPS
			r.hasDecodeSample = true
		} else {
			r.decodeTPSEMA = latencyEMAAlpha*decodeTPS + (1-latencyEMAAlpha)*r.decodeTPSEMA
		}
	}
	r.statsMu.Unlock()
}

func (r *Runtime) healthSnapshot() (float64, int64, int64, float64) {
	load := float64(r.inflightInference.Load())
	r.statsMu.RLock()
	lat := int64(0)
	if r.hasLatencySample {
		lat = int64(r.latencyEMAms + 0.5)
	}
	ttft := int64(0)
	if r.hasTTFTSample {
		ttft = int64(r.ttftEMAms + 0.5)
	}
	decodeTPS := float64(0)
	if r.hasDecodeSample {
		decodeTPS = r.decodeTPSEMA
	}
	r.statsMu.RUnlock()
	return load, lat, ttft, decodeTPS
}
