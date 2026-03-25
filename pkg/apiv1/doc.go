// Package apiv1 holds protobuf-generated types for the v0.1 coordination protocol.
//
//go:generate sh -c "cd ../.. && protoc -I proto proto/tooti/v0/coordination.proto --go_out=. --go_opt=module=github.com/mrostamii/tooti"
package apiv1
