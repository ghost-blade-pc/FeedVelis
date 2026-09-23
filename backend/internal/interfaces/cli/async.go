package cli

import (
	"context"
	"flag"
	"fmt"
)

func (r *Runner) runAsync(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("缺少 async 命令\n%s", usage)
	}
	switch args[0] {
	case "backfill-articles":
		return r.runBackfill(ctx, args[1:])
	case "replay-dlq":
		return r.runReplayDLQ(ctx, args[1:])
	default:
		return fmt.Errorf("不支持的 async 命令 %q\n%s", args[0], usage)
	}
}

func (r *Runner) runReplayDLQ(ctx context.Context, args []string) error {
	if r.options.Replay == nil {
		return fmt.Errorf("DLQ 重放未装配")
	}
	flags := flag.NewFlagSet("async replay-dlq", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	limit := flags.Int("limit", 0, "最多重放消息数（1..1000，必填）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 1000 {
		return fmt.Errorf("-limit 必须介于 1 和 1000")
	}
	report, err := r.options.Replay.Run(ctx, *limit)
	fmt.Fprintf(r.stdout(), "confirmed=%d failed=%d\n", report.Confirmed, report.Failed)
	return err
}
func (r *Runner) runBackfill(ctx context.Context, args []string) error {
	if r.options.Backfill == nil {
		return fmt.Errorf("文章异步补录未装配")
	}
	flags := flag.NewFlagSet("async backfill-articles", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	limit := flags.Int("limit", 0, "最多处理文章数（1..1000，必填）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > 1000 {
		return fmt.Errorf("-limit 必须介于 1 和 1000")
	}
	report, err := r.options.Backfill.Run(ctx, *limit)
	fmt.Fprintf(r.stdout(), "created=%d skipped=%d failed=%d remaining=%t\n", report.Created, report.Skipped, report.Failed, report.HasMore)
	return err
}
