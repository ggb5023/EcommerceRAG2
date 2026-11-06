package ports

import (
	"context"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
)

type fakeIntelClient struct {
	request *ragv1.ListObservationsRequest
}

func (f *fakeIntelClient) ListObservations(_ context.Context, in *ragv1.ListObservationsRequest,
	_ ...grpc.CallOption) (*ragv1.ListObservationsResponse, error) {
	f.request = in
	return &ragv1.ListObservationsResponse{Items: []*ragv1.Observation{{Market: "m"}}}, nil
}

var _ IntelClient = (*fakeIntelClient)(nil)

func TestIntelClientPortShape(t *testing.T) {
	client := &fakeIntelClient{}
	var port IntelClient = client
	response, err := port.ListObservations(context.Background(), &ragv1.ListObservationsRequest{
		Market: "m", Category: "c", Platform: "p", Since: "2026-11-01T00:00:00Z", Limit: 10,
	})
	if err != nil || len(response.Items) != 1 || response.Items[0].Market != "m" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if client.request.Limit != 10 {
		t.Fatalf("request was not passed through: %+v", client.request)
	}
}
