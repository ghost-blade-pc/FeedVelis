// Package cli 提供仅限本地执行的 Source 管理协议适配器。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

type SourceService interface {
	Add(context.Context, string) (sourceDomain.Source, bool, error)
	List(context.Context) ([]sourceDomain.Source, error)
	Pause(context.Context, int64) error
	Resume(context.Context, int64) error
	FetchByID(context.Context, int64, bool) (sourceApp.FetchOutcome, error)
}

type Runner struct {
	service SourceService
	stdout  io.Writer
	stderr  io.Writer
}

func New(service SourceService, stdout, stderr io.Writer) *Runner {
	return &Runner{service: service, stdout: stdout, stderr: stderr}
}

func (r *Runner) Run(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "source" {
		return errors.New("用法: velis-admin source <add|list|pause|resume|fetch <id> [--force]> [参数]")
	}
	switch args[1] {
	case "add":
		flags := flag.NewFlagSet("source add", flag.ContinueOnError)
		flags.SetOutput(r.stderr)
		rawURL := flags.String("url", "", "Feed URL")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if *rawURL == "" {
			return errors.New("source add 需要 -url")
		}
		src, inserted, err := r.service.Add(ctx, *rawURL)
		if err != nil {
			return err
		}
		fmt.Fprintf(r.stdout, "source_id=%d created=%t\n", src.ID, inserted)
		return nil
	case "list":
		items, err := r.service.List(ctx)
		if err != nil {
			return err
		}
		for _, src := range items {
			fmt.Fprintf(r.stdout, "%d\t%s\t%s\t%s\n", src.ID, src.Status, src.Title, src.FeedURL)
		}
		return nil
	case "pause", "resume", "fetch":
		force := false
		idArgs := args[2:]
		if len(idArgs) == 2 && idArgs[1] == "--force" {
			force = true
			idArgs = idArgs[:1]
		}
		id, err := parseID(idArgs)
		if err != nil {
			return err
		}
		switch args[1] {
		case "pause":
			err = r.service.Pause(ctx, id)
		case "resume":
			err = r.service.Resume(ctx, id)
		case "fetch":
			var outcome sourceApp.FetchOutcome
			outcome, err = r.service.FetchByID(ctx, id, force)
			if err == nil {
				fmt.Fprintf(r.stdout, "source_id=%d not_modified=%t inserted=%d updated=%d unchanged=%d\n", id, outcome.NotModified, outcome.Report.Inserted, outcome.Report.Updated, outcome.Report.Unchanged)
			}
		}
		return err
	default:
		return fmt.Errorf("不支持的 source 命令 %q", args[1])
	}
}

func parseID(args []string) (int64, error) {
	if len(args) != 1 {
		return 0, errors.New("命令需要一个 source id")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("source id 必须是正整数")
	}
	return id, nil
}
