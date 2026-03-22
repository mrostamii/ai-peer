// Package apiv1 holds protobuf-generated types for the v0.1 coordination protocol.
//
//go:generate sh -c "cd ../.. && protoc -I proto proto/ai_peer/v0/coordination.proto --go_out=. --go_opt=module=github.com/mrostamii/ai-peer"
package apiv1
