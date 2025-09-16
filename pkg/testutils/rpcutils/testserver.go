// Copyright 2021 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package rpcutils

import (
	"context"
	any1 "github.com/golang/protobuf/ptypes/any"

	"github.com/gogo/protobuf/types"
)

// TestServerImpl backs the Test service.
type TestServerImpl struct {
	UU func(context.Context, *types.Any) (*types.Any, error) // UnaryUnary
	US func(*types.Any, Test_UnaryStreamServer) error        // UnaryStream
	SU func(server Test_StreamUnaryServer) error             // StreamUnary
	SS func(server Test_StreamStreamServer) error            // StreamStream
}

type DRPCTestServerImpl struct {
	UU func(context.Context, *any1.Any) (*any1.Any, error) // UnaryUnary
	US func(*any1.Any, DRPCTest_UnaryStreamStream) error   // UnaryStream
	SU func(server DRPCTest_StreamUnaryStream) error       // StreamUnary
	SS func(server DRPCTest_StreamStreamStream) error      // StreamStream
}

var _ TestServer = (*TestServerImpl)(nil)
var _ DRPCTestServer = (*DRPCTestServerImpl)(nil)

// UnaryUnary implements GRPCTestServer.
func (s *TestServerImpl) UnaryUnary(ctx context.Context, any *types.Any) (*types.Any, error) {
	return s.UU(ctx, any)
}

// UnaryStream implements GRPCTestServer.
func (s *TestServerImpl) UnaryStream(any *types.Any, server Test_UnaryStreamServer) error {
	return s.US(any, server)
}

// StreamUnary implements GRPCTestServer.
func (s *TestServerImpl) StreamUnary(server Test_StreamUnaryServer) error {
	return s.SU(server)
}

// StreamStream implements GRPCTestServer.
func (s *TestServerImpl) StreamStream(server Test_StreamStreamServer) error {
	return s.SS(server)
}

// UnaryUnary implements DRPCTestServer.
func (s *DRPCTestServerImpl) UnaryUnary(ctx context.Context, any *any1.Any) (*any1.Any, error) {
	return s.UU(ctx, any)
}

// UnaryStream implements DRPCTestServer.
func (s *DRPCTestServerImpl) UnaryStream(any *any1.Any, server DRPCTest_UnaryStreamStream) error {
	return s.US(any, server)
}

// StreamUnary implements DRPCTestServer.
func (s *DRPCTestServerImpl) StreamUnary(server DRPCTest_StreamUnaryStream) error {
	return s.SU(server)
}

// StreamStream implements DRPCTestServer.
func (s *DRPCTestServerImpl) StreamStream(server DRPCTest_StreamStreamStream) error {
	return s.SS(server)
}
