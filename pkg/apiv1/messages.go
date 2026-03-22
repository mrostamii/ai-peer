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

// ChatMessage is one role/content item in an inference conversation.
type ChatMessage struct {
	Role    string
	Content string
}

func (m *ChatMessage) GetRole() string {
	if m == nil {
		return ""
	}
	return m.Role
}

func (m *ChatMessage) GetContent() string {
	if m == nil {
		return ""
	}
	return m.Content
}

// InferenceRequest is the v0.1 libp2p request payload for remote inference.
type InferenceRequest struct {
	RequestId string
	Model     string
	Messages  []*ChatMessage
	Params    map[string]string
}

func (m *InferenceRequest) GetRequestId() string {
	if m == nil {
		return ""
	}
	return m.RequestId
}

func (m *InferenceRequest) GetModel() string {
	if m == nil {
		return ""
	}
	return m.Model
}

func (m *InferenceRequest) GetMessages() []*ChatMessage {
	if m == nil {
		return nil
	}
	return m.Messages
}

func (m *InferenceRequest) GetParams() map[string]string {
	if m == nil {
		return nil
	}
	return m.Params
}

// InferenceResponse is the v0.1 libp2p response payload.
type InferenceResponse struct {
	RequestId    string
	Content      string
	TokensUsed   int64
	LatencyMs    int64
	Ok           bool
	ErrorMessage string
}

func (m *InferenceResponse) GetRequestId() string {
	if m == nil {
		return ""
	}
	return m.RequestId
}

func (m *InferenceResponse) GetContent() string {
	if m == nil {
		return ""
	}
	return m.Content
}

func (m *InferenceResponse) GetTokensUsed() int64 {
	if m == nil {
		return 0
	}
	return m.TokensUsed
}

func (m *InferenceResponse) GetLatencyMs() int64 {
	if m == nil {
		return 0
	}
	return m.LatencyMs
}

func (m *InferenceResponse) GetOk() bool {
	if m == nil {
		return false
	}
	return m.Ok
}

func (m *InferenceResponse) GetErrorMessage() string {
	if m == nil {
		return ""
	}
	return m.ErrorMessage
}
