package server

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/c12s/gprunner"
	"github.com/c12s/gprunner/cmd/api"
	"github.com/c12s/gprunner/cmd/starmap"
	"github.com/c12s/gprunner/pkg/model"
)

type Server struct {
	api.UnimplementedRunnerServiceServer

	runner  *gprunner.Runner
	starmap *starmap.Client
	logger  *slog.Logger
}

func New(runner *gprunner.Runner, sm *starmap.Client, l *slog.Logger) *Server {
	return &Server{runner: runner, starmap: sm, logger: l}
}

// Pull fetches the chart from starmap and saves it, replacing any earlier copy.
func (s *Server) Pull(ctx context.Context, ref *api.ChartRef) (*api.Empty, error) {
	if _, err := s.pull(ctx, ref); err != nil {
		return nil, err
	}

	return &api.Empty{}, nil
}

// Instantiate starts a saved chart, re-pulling it first when asked to.
func (s *Server) Instantiate(ctx context.Context, req *api.InstantiateReq) (*api.Empty, error) {
	ref, err := toRef(req.GetRef())
	if err != nil {
		return nil, err
	}

	if req.GetPull() {
		chart, err := s.pull(ctx, req.GetRef())
		if err != nil {
			return nil, err
		}
		ref.SchemaVersion = chart.SchemaVersion
	}

	s.logger.Info("instantiating chart", "chart", ref.Name, "version", ref.SchemaVersion)
	if err := s.runner.InstantiateChart(ctx, ref); err != nil {
		s.logger.Error("instantiate chart", "chart", ref.Name, "err", err)
		return nil, toStatus(err)
	}

	return &api.Empty{}, nil
}

// Kill stops a running chart.
func (s *Server) Kill(ctx context.Context, apiRef *api.ChartRef) (*api.Empty, error) {
	ref, err := toRef(apiRef)
	if err != nil {
		return nil, err
	}

	s.logger.Info("killing chart", "chart", ref.Name, "version", ref.SchemaVersion)
	if err := s.runner.KillChart(ctx, ref); err != nil {
		s.logger.Error("kill chart", "chart", ref.Name, "err", err)
		return nil, toStatus(err)
	}

	return &api.Empty{}, nil
}

// pull is the shared fetch-and-save step of Pull and Instantiate.
func (s *Server) pull(ctx context.Context, apiRef *api.ChartRef) (model.Chart, error) {
	ref, err := toRef(apiRef)
	if err != nil {
		return model.Chart{}, err
	}

	chart, err := s.starmap.GetChart(ctx, ref)
	if err != nil {
		s.logger.Error("fetch chart", "chart", ref.Name, "err", err)
		// starmap's own code (NotFound, Unavailable...) is the useful one
		if st, ok := status.FromError(errors.Unwrap(err)); ok {
			return model.Chart{}, st.Err()
		}
		return model.Chart{}, status.Error(codes.Internal, err.Error())
	}

	if err := s.runner.SaveChart(ctx, chart); err != nil {
		s.logger.Error("save chart", "chart", ref.Name, "err", err)
		return model.Chart{}, toStatus(err)
	}

	s.logger.Info("chart pulled", "chart", chart.Metadata.Name, "id", chart.Metadata.ID, "version", chart.SchemaVersion)
	return chart, nil
}

// toRef validates the wire reference and converts it to the library's type.
func toRef(ref *api.ChartRef) (model.ChartRef, error) {
	if ref.GetName() == "" || ref.GetMaintainer() == "" || ref.GetSchemaVersion() == "" || ref.GetNamespace() == "" {
		return model.ChartRef{}, status.Error(codes.InvalidArgument, "required arguments: name, version, maintainer, namespace")
	}

	return model.ChartRef{
		Name:          ref.GetName(),
		Namespace:     ref.GetNamespace(),
		Maintainer:    ref.GetMaintainer(),
		SchemaVersion: ref.GetSchemaVersion(),
	}, nil
}

// toStatus maps library failures onto gRPC codes.
func toStatus(err error) error {
	switch {
	case errors.Is(err, gprunner.ErrChartNotPulled):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
