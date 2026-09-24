package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

func (r *Runner) runSource(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: velis-admin source <add|list|pause|resume|fetch <id> [--force]|runs <id> [--limit N]> [参数]")
	}
	if r.options.Sources == nil {
		return errors.New("当前进程未装配 Source 用例")
	}
	service := r.options.Sources
	switch args[0] {
	case "add":
		flags := flag.NewFlagSet("source add", flag.ContinueOnError)
		flags.SetOutput(r.stderr())
		rawURL := flags.String("url", "", "Feed URL")
		interval := flags.Duration("interval", 0, "抓取周期，例如 15m；省略时使用默认 30m")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *rawURL == "" {
			return errors.New("source add 需要 -url")
		}
		// 省略 -interval 时补齐领域默认周期：0 会被 ValidateFetchInterval 拒绝，
		// 而 AddWithInterval 不会像 Add 那样自行填充默认值。
		if *interval == 0 {
			*interval = sourceDomain.DefaultFetchInterval
		}
		src, inserted, err := service.AddWithInterval(ctx, *rawURL, *interval)
		if err != nil {
			return err
		}
		fmt.Fprintf(r.stdout(), "source_id=%d created=%t interval=%s version=%d\n",
			src.ID, inserted, src.FetchIntervalOr(), src.LockVersion)
		return nil
	case "list":
		items, err := service.List(ctx)
		if err != nil {
			return err
		}
		for _, src := range items {
			fmt.Fprintf(r.stdout(), "%d\t%s\t%s\t%s\t周期=%s\t版本=%d\t下次抓取=%s\t连续失败=%d\n",
				src.ID, src.Status, src.Title, src.FeedURL, src.FetchIntervalOr(), src.LockVersion,
				src.NextFetchAt.UTC().Format(time.RFC3339), src.ConsecutiveFailures)
		}
		return nil
	case "runs":
		// id 在标志之前：与 pause/resume/fetch 的位置参数习惯一致。
		if len(args) < 2 {
			return errors.New("source runs 需要一个 source id")
		}
		id, err := parseID(args[1:2])
		if err != nil {
			return err
		}
		flags := flag.NewFlagSet("source runs", flag.ContinueOnError)
		flags.SetOutput(r.stderr())
		limit := flags.Int("limit", 20, "最多展示的运行条数")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		page, err := service.History(ctx, sourceApp.HistoryCommand{SourceID: id, Limit: *limit})
		if err != nil {
			return err
		}
		for _, run := range page.Items {
			errorCode := "-"
			if run.ErrorCode != nil {
				errorCode = *run.ErrorCode
			}
			fmt.Fprintf(r.stdout(), "%s\t%s\t%s\t304=%t\t插入=%d\t更新=%d\t未变=%d\t跳过=%d\t开始=%s\t错误=%s\n",
				run.ID, run.Trigger, run.Status, run.NotModified, run.Inserted, run.Updated,
				run.Unchanged, run.Skipped, run.StartedAt.UTC().Format(time.RFC3339), errorCode)
		}
		return nil
	case "pause", "resume", "fetch":
		force := false
		idArgs := args[1:]
		if len(idArgs) == 2 && idArgs[1] == "--force" {
			force = true
			idArgs = idArgs[:1]
		}
		id, err := parseID(idArgs)
		if err != nil {
			return err
		}
		switch args[0] {
		case "pause", "resume":
			// 乐观锁参数由 CLI 自行读取当前版本；管理员并发修改时以版本冲突结束。
			var current sourceDomain.Source
			if current, err = service.Get(ctx, id); err != nil {
				return err
			}
			var updated sourceDomain.Source
			if args[0] == "pause" {
				updated, err = service.Pause(ctx, id, current.LockVersion)
			} else {
				updated, err = service.Resume(ctx, id, current.LockVersion)
			}
			if err == nil {
				fmt.Fprintf(r.stdout(), "source_id=%d status=%s version=%d next_fetch=%s\n",
					updated.ID, updated.Status, updated.LockVersion, updated.NextFetchAt.UTC().Format(time.RFC3339))
			}
		case "fetch":
			var outcome sourceApp.FetchOutcome
			var run sourceDomain.FetchRun
			outcome, run, err = service.FetchManual(ctx, sourceApp.ManualFetch{SourceID: id, Force: force})
			if err == nil {
				fmt.Fprintf(r.stdout(), "run_id=%s status=%s not_modified=%t inserted=%d updated=%d unchanged=%d skipped=%d\n",
					run.ID, run.Status, outcome.NotModified, outcome.Report.Inserted, outcome.Report.Updated,
					outcome.Report.Unchanged, totalSkipped(outcome.Report))
			}
		}
		return err
	default:
		return fmt.Errorf("不支持的 source 命令 %q", args[0])
	}
}

// totalSkipped 汇总被跳过的条目原因计数，便于 CLI 单行展示。
func totalSkipped(report articleApp.IngestReport) int {
	total := 0
	for _, count := range report.Skipped {
		total += count
	}
	return total
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
