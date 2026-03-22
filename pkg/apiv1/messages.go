package apiv1

// NodeAnnounce mirrors the v0.1 coordination NodeAnnounce protobuf payload.
type NodeAnnounce struct {
	NodeId          string
	Models          []string
	HardwareSummary string
	LocationHint    string
	PricingHint     string
	TimestampMs     int64
}

func (m *NodeAnnounce) GetNodeId() string {
	if m == nil {
		return ""
	}
	return m.NodeId
}

func (m *NodeAnnounce) GetModels() []string {
	if m == nil {
		return nil
	}
	return m.Models
}

func (m *NodeAnnounce) GetHardwareSummary() string {
	if m == nil {
		return ""
	}
	return m.HardwareSummary
}

func (m *NodeAnnounce) GetLocationHint() string {
	if m == nil {
		return ""
	}
	return m.LocationHint
}

func (m *NodeAnnounce) GetPricingHint() string {
	if m == nil {
		return ""
	}
	return m.PricingHint
}

func (m *NodeAnnounce) GetTimestampMs() int64 {
	if m == nil {
		return 0
	}
	return m.TimestampMs
}

// HealthUpdate mirrors the v0.1 coordination HealthUpdate protobuf payload.
type HealthUpdate struct {
	NodeId      string
	UptimeSec   int64
	Load        float64
	LatencyMs   int64
	TimestampMs int64
}

func (m *HealthUpdate) GetNodeId() string {
	if m == nil {
		return ""
	}
	return m.NodeId
}

func (m *HealthUpdate) GetUptimeSec() int64 {
	if m == nil {
		return 0
	}
	return m.UptimeSec
}

func (m *HealthUpdate) GetLoad() float64 {
	if m == nil {
		return 0
	}
	return m.Load
}

func (m *HealthUpdate) GetLatencyMs() int64 {
	if m == nil {
		return 0
	}
	return m.LatencyMs
}

func (m *HealthUpdate) GetTimestampMs() int64 {
	if m == nil {
		return 0
	}
	return m.TimestampMs
}
