package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

type cliSourceService struct {
	addedURL string
	pausedID int64
}

func (s *cliSourceService) Add(_ context.Context, rawURL string) (sourceDomain.Source, bool, error) {
	s.addedURL = rawURL
	return sourceDomain.Source{ID: 9}, true, nil
}
func (*cliSourceService) List(context.Context) ([]sourceDomain.Source, error) { return nil, nil }
func (s *cliSourceService) Pause(_ context.Context, id int64) error {
	s.pausedID = id
	return nil
}
func (*cliSourceService) Resume(context.Context, int64) error { return nil }
func (*cliSourceService) FetchByID(context.Context, int64) (sourceApp.FetchOutcome, error) {
	return sourceApp.FetchOutcome{}, nil
}

func TestSourceAddAndPause(t *testing.T) {
	service := &cliSourceService{}
	var stdout bytes.Buffer
	runner := New(service, &stdout, &bytes.Buffer{})
	if err := runner.Run(context.Background(), []string{"source", "add", "-url", "https://example.com/feed"}); err != nil {
		t.Fatal(err)
	}
	if service.addedURL != "https://example.com/feed" || !strings.Contains(stdout.String(), "source_id=9 created=true") {
		t.Fatalf("url=%q output=%q", service.addedURL, stdout.String())
	}
	if err := runner.Run(context.Background(), []string{"source", "pause", "9"}); err != nil {
		t.Fatal(err)
	}
	if service.pausedID != 9 {
		t.Fatalf("paused=%d", service.pausedID)
	}
}

func TestSourceCommandRejectsInvalidID(t *testing.T) {
	runner := New(&cliSourceService{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err := runner.Run(context.Background(), []string{"source", "fetch", "0"}); err == nil {
		t.Fatal("expected invalid id")
	}
}
