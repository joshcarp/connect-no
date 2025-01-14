// Copyright 2021-2023 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package connect_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/joshcarp/connect-no/internal/assert"
)

type panicPingServer struct {
	pingv1connect_test.UnimplementedPingServiceHandler

	panicWith any
}

func (s *panicPingServer) Ping(
	context.Context,
	*connect.Request[pingv1_test.PingRequest],
) (*connect.Response[pingv1_test.PingResponse], error) {
	panic(s.panicWith) //nolint:forbidigo
}

func (s *panicPingServer) CountUp(
	_ context.Context,
	_ *connect.Request[pingv1_test.CountUpRequest],
	stream *connect.ServerStream[pingv1_test.CountUpResponse],
) error {
	if err := stream.Send(&pingv1_test.CountUpResponse{}); err != nil {
		return err
	}
	panic(s.panicWith) //nolint:forbidigo
}

func TestWithRecover(t *testing.T) {
	t.Parallel()
	handle := func(_ context.Context, _ connect.Spec, _ http.Header, r any) error {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("panic: %v", r))
	}
	assertHandled := func(err error) {
		t.Helper()
		assert.NotNil(t, err)
		assert.Equal(t, connect.CodeOf(err), connect.CodeFailedPrecondition)
	}
	assertNotHandled := func(err error) {
		t.Helper()
		// When HTTP/2 handlers panic, net/http sends an RST_STREAM frame with code
		// INTERNAL_ERROR. We should be mapping this back to CodeInternal.
		assert.Equal(t, connect.CodeOf(err), connect.CodeInternal)
	}
	drainStream := func(stream *connect.ServerStreamForClient[pingv1_test.CountUpResponse]) error {
		t.Helper()
		defer stream.Close()
		assert.True(t, stream.Receive())  // expect one response msg
		assert.False(t, stream.Receive()) // expect panic before second response msg
		return stream.Err()
	}
	pinger := &panicPingServer{}
	mux := http.NewServeMux()
	mux.Handle(pingv1connect_test.NewPingServiceHandler(pinger, connect.WithRecover(handle)))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := pingv1connect_test.NewPingServiceClient(
		server.Client(),
		server.URL,
	)

	for _, panicWith := range []any{42, nil} {
		pinger.panicWith = panicWith

		_, err := client.Ping(context.Background(), connect.NewRequest(&pingv1_test.PingRequest{}))
		assertHandled(err)

		stream, err := client.CountUp(context.Background(), connect.NewRequest(&pingv1_test.CountUpRequest{}))
		assert.Nil(t, err)
		assertHandled(drainStream(stream))
	}

	pinger.panicWith = http.ErrAbortHandler

	_, err := client.Ping(context.Background(), connect.NewRequest(&pingv1_test.PingRequest{}))
	assertNotHandled(err)

	stream, err := client.CountUp(context.Background(), connect.NewRequest(&pingv1_test.CountUpRequest{}))
	assert.Nil(t, err)
	assertNotHandled(drainStream(stream))
}
