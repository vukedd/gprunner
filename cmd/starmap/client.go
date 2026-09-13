// Package starmap is the runner service's client for the starmap chart
// registry. It is the only place the service talks to starmap; the library
// never does.
package starmap

import (
	"context"
	"encoding/json"
	"fmt"

	starmapapi "github.com/c12s/starmap/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/c12s/gprunner/pkg/model"
)

type Client struct {
	conn *grpc.ClientConn
	reg  starmapapi.RegistryServiceClient
}

// Dial prepares a connection to starmap at addr (host:port). gRPC connects
// lazily, so this never blocks and an unreachable starmap only surfaces on the
// first call. Plaintext, since both services sit on the same private network.
func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("starmap: dialing %s: %w", addr, err)
	}

	return &Client{conn: conn, reg: starmapapi.NewRegistryServiceClient(conn)}, nil
}

// Close releases the connection and every stream on it.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) GetChart(ctx context.Context, ref model.ChartRef) (model.Chart, error) {
	resp, err := c.reg.GetChartMetadata(ctx, &starmapapi.GetChartFromMetadataReq{
		Name:          ref.Name,
		Namespace:     ref.Namespace,
		Maintainer:    ref.Maintainer,
		SchemaVersion: ref.SchemaVersion,
	})
	if err != nil {
		return model.Chart{}, fmt.Errorf("starmap: fetching chart %s/%s@%q for %s: %w", ref.Namespace, ref.Name, ref.SchemaVersion, ref.Maintainer, err)
	}

	return toModel(resp)
}

func toModel(resp *starmapapi.GetChartResp) (model.Chart, error) {
	raw, err := protojson.Marshal(resp)
	if err != nil {
		return model.Chart{}, fmt.Errorf("starmap: encoding chart: %w", err)
	}

	var chart model.Chart
	if err := json.Unmarshal(raw, &chart); err != nil {
		return model.Chart{}, fmt.Errorf("starmap: decoding chart: %w", err)
	}

	if chart.Metadata.ID == "" {
		return model.Chart{}, fmt.Errorf("starmap: chart %q came back without an id", chart.Metadata.Name)
	}

	return chart, nil
}
