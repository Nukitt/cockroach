// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package drpcinterceptor_test

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/testutils/rpcutils"
	"github.com/golang/protobuf/ptypes"
	any1 "github.com/golang/protobuf/ptypes/any"
	"io"
	"net"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/util"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/tracing"
	"github.com/cockroachdb/cockroach/pkg/util/tracing/drpcinterceptor"
	"github.com/cockroachdb/cockroach/pkg/util/tracing/tracingpb"
	"github.com/cockroachdb/cockroach/pkg/util/tracing/tracingutil"
	"github.com/cockroachdb/errors"
	"github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/require"
	"storj.io/drpc/drpcclient"
	"storj.io/drpc/drpcconn"
	"storj.io/drpc/drpcmux"
	"storj.io/drpc/drpcserver"
)

var _ tracing.Structured = &tracingutil.TestStructuredImpl{}

// TestDRPCInterceptors provides end-to-end verification of the DRPC tracing
// interceptors. It sets up a client and a server, both configured with tracing
// interceptors, and tests all four RPC types (unary, streaming). The test
// ensures that a client-side span is correctly propagated to the server,
// creating a parent-child relationship between the client and server spans.
func TestDRPCInterceptors(t *testing.T) {
	defer leaktest.AfterTest(t)()

	const magicValue = "magic-value"

	// checkForSpanAndReturnRecording is a helper used by the server RPC
	// implementations. It verifies that a span is present in the context,
	// records a structured event into it, and returns the serialized span
	// recording. This allows the client to verify that the server-side logic
	// was executed within the correct trace.
	checkForSpanAndReturnRecording := func(ctx context.Context) (*any1.Any, error) {
		sp := tracing.SpanFromContext(ctx)
		if sp == nil {
			return nil, errors.New("no span in ctx")
		}
		sp.RecordStructured(tracingutil.NewTestStructured(magicValue))
		recs := sp.GetRecording(tracingpb.RecordingVerbose)
		if len(recs) != 1 {
			return nil, errors.Newf("expected exactly one recorded span, not %+v", recs)
		}
		return ptypes.MarshalAny(&recs[0])
	}

	// The server implementation for each RPC type will check for a span and
	// return its recording.
	impl := &rpcutils.DRPCTestServerImpl{
		UU: func(ctx context.Context, any *any1.Any) (*any1.Any, error) {
			return checkForSpanAndReturnRecording(ctx)
		},
		US: func(_ *any1.Any, server rpcutils.DRPCTest_UnaryStreamStream) error {
			any, err := checkForSpanAndReturnRecording(server.Context())
			if err != nil {
				return err
			}
			return server.Send(any)
		},
		SU: func(server rpcutils.DRPCTest_StreamUnaryStream) error {
			var req any1.Any
			if err := server.RecvMsg(&req); err != nil {
				return err
			}
			any, err := checkForSpanAndReturnRecording(server.Context())
			if err != nil {
				return err
			}
			return server.SendAndClose(any)
		},
		SS: func(server rpcutils.DRPCTest_StreamStreamStream) error {
			var req types.Any
			if err := server.RecvMsg(&req); err != nil {
				return err
			}
			any, err := checkForSpanAndReturnRecording(server.Context())
			if err != nil {
				return err
			}
			return server.Send(any)
		},
	}

	unusedAny, err := ptypes.MarshalAny(&ptypes.Empty{})
	require.NoError(t, err)

	// Define the test cases for each RPC type.
	for _, tc := range []struct {
		name string
		do   func(context.Context, rpcutils.DRPCTestClient) (*types.Any, error)
	}{
		{
			name: "UnaryUnary",
			do: func(ctx context.Context, c rpcutils.DRPCTestClient) (*types.Any, error) {
				return c.UnaryUnary(ctx, unusedAny)
			},
		},
		{
			name: "UnaryStream",
			do: func(ctx context.Context, c rpcutils.DRPCTestClient) (*types.Any, error) {
				sc, err := c.NewStream(ctx, usRPC, protoEncoding{})
				if err != nil {
					return nil, err
				}
				if err := sc.MsgSend(unusedAny, protoEncoding{}); err != nil {
					return nil, err
				}
				if err := sc.CloseSend(); err != nil {
					return nil, err
				}
				var first *types.Any
				for {
					any := new(types.Any)
					if err := sc.MsgRecv(any, protoEncoding{}); err != nil {
						if err == io.EOF {
							break
						}
						return nil, err
					}
					if first == nil {
						first = any
					}
				}
				return first, nil
			},
		},
		{
			name: "StreamUnary",
			do: func(ctx context.Context, c rpcutils.DRPCTestClient) (*types.Any, error) {
				sc, err := c.NewStream(ctx, suRPC, protoEncoding{})
				if err != nil {
					return nil, err
				}
				if err := sc.MsgSend(unusedAny, protoEncoding{}); err != nil {
					return nil, err
				}
				if err := sc.CloseSend(); err != nil {
					return nil, err
				}
				out := new(types.Any)
				// The single response message is received here.
				if err := sc.MsgRecv(out, protoEncoding{}); err != nil {
					return nil, err
				}
				if err := sc.Close(); err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		{
			name: "StreamStream",
			do: func(ctx context.Context, c rpcutils.DRPCTestClient) (*types.Any, error) {
				sc, err := c.NewStream(ctx, ssRPC, protoEncoding{})
				if err != nil {
					return nil, err
				}
				if err := sc.MsgSend(unusedAny, protoEncoding{}); err != nil {
					return nil, err
				}
				if err := sc.CloseSend(); err != nil {
					return nil, err
				}
				var first *types.Any
				for {
					any := new(types.Any)
					if err := sc.MsgRecv(any, protoEncoding{}); err != nil {
						if err == io.EOF {
							break
						}
						return nil, err
					}
					if first == nil {
						first = any
					}
				}
				if err := sc.Close(); err != nil {
					return nil, err
				}
				return first, nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Setup: Create a tracer, server mux with interceptors, and a server.
			bgCtx := context.Background()
			tr := tracing.NewTracer()
			mux := drpcmux.NewWithInterceptors(
				[]drpcmux.UnaryServerInterceptor{drpcinterceptor.ServerInterceptor(tr)},
				[]drpcmux.StreamServerInterceptor{drpcinterceptor.StreamServerInterceptor(tr)},
			)
			require.NoError(t, rpcutils.DRPCRegisterTest(mux, impl))
			srv := drpcserver.New(mux)
			ln, err := net.Listen(util.TestAddr.Network(), util.TestAddr.String())
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(bgCtx)
			defer cancel()
			go func() {
				// Serve requests in a background goroutine.
				if err := srv.Serve(ctx, ln); err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			}()

			rawconn, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
			require.NoError(t, err)
			conn := drpcconn.New(rawconn)
			cc, err := drpcclient.NewClientConnWithOptions(bgCtx, conn,
				drpcclient.WithChainUnaryInterceptor(
					drpcinterceptor.ClientInterceptor(tr, nil)),
				drpcclient.WithChainStreamInterceptor(
					drpcinterceptor.StreamClientInterceptor(tr, nil)),
			)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()

			ctxSpan, sp := tr.StartSpanCtx(
				bgCtx,
				"root",
				tracing.WithRecording(tracingpb.RecordingVerbose),
			)
			recAny, err := tc.do(ctxSpan, cc)
			require.NoError(t, err)

			var rec tracingpb.RecordedSpan
			require.NoError(t, types.UnmarshalAny(recAny, &rec))
			require.Len(t, rec.StructuredRecords, 1)

			sp.ImportRemoteRecording([]tracingpb.RecordedSpan{rec})
			finalRecs := sp.FinishAndGetRecording(tracingpb.RecordingVerbose)

			var n int
			for i := range finalRecs {
				rec := &finalRecs[i]
				n += len(rec.StructuredRecords)
				anonymousTagGroup := rec.FindTagGroup(tracingpb.AnonymousTagGroupName)
				if anonymousTagGroup == nil {
					continue
				}
				filtered := make([]tracingpb.Tag, 0, len(anonymousTagGroup.Tags))
				for _, tag := range anonymousTagGroup.Tags {
					if tag.Key == "_unfinished" || tag.Key == "_verbose" {
						continue
					}
					filtered = append(filtered, tag)
				}
				anonymousTagGroup.Tags = filtered
			}
			require.Equal(t, 1, n)

			expSpanName := tc.name
			exp := fmt.Sprintf(`
                span: root
                    span: /cockroach.testutils.rpcutils.DRPCTest/%[1]s
                        tags: span.kind=client
                    span: /cockroach.testutils.rpcutils.DRPCTest/%[1]s
                        tags: span.kind=server
                        event: structured=magic-value`, expSpanName)
			require.NoError(t, tracing.CheckRecordedSpans(finalRecs, exp))

			// Check that no spans were leaked.
			testutils.SucceedsSoon(t, func() error {
				return tr.VisitSpans(func(sp tracing.RegistrySpan) error {
					rec := sp.GetFullRecording(tracingpb.RecordingVerbose).Root
					return errors.Newf("leaked span: %s %s", rec.Operation, rec.TagGroups)
				})
			})
		})
	}
}
