package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
)

const searchUsage = `  search retry [-article-id <id>] [-index <physical-index>] -limit <1..1000>
  search index init
  search rebuild start [<rebuild-id>]
  search rebuild resume [<rebuild-id>]
  search rebuild status [-limit <1..20>]
  search rebuild cutover [<rebuild-id>]
  search rebuild rollback [<rebuild-id>]
  search rebuild abandon [<rebuild-id>]
  search rebuild cleanup -index <physical-index> -confirm`

// SearchService 是搜索投影的本地管理入口：失败重试、索引初始化与重建。
type SearchService interface {
	Retry(context.Context, projectionApp.RetryScope, time.Time) (projectionApp.RetryReport, error)
	InitIndex(context.Context) (projectionApp.IndexState, error)
	Start(context.Context) (projectionApp.RebuildState, error)
	Resume(context.Context, string) (projectionApp.RebuildState, error)
	Status(context.Context, int) (projectionApp.IndexStatus, error)
	Cutover(context.Context, string) (projectionApp.RebuildState, error)
	Rollback(context.Context, string) (projectionApp.RebuildState, error)
	Abandon(context.Context, string) (projectionApp.RebuildState, error)
	Cleanup(context.Context, string, bool) error
}

func (r *Runner) runSearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("search 需要子命令\n%s", usage)
	}
	switch args[0] {
	case "retry":
		return r.runSearchRetry(ctx, args[1:])
	case "index":
		return r.runSearchIndex(ctx, args[1:])
	case "rebuild":
		return r.runSearchRebuild(ctx, args[1:])
	default:
		return fmt.Errorf("不支持的 search 子命令 %q\n%s", args[0], usage)
	}
}

// runSearchRetry 只重新激活仍处于当前 generation 的失败 delivery，
// 并输出可审计报告：范围、跳过数量与精确目标。
func (r *Runner) runSearchRetry(ctx context.Context, args []string) error {
	if r.options.Search == nil {
		return errors.New("未配置 OpenSearch，无法重试搜索投影投递")
	}
	flags := flag.NewFlagSet("search retry", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	articleID := flags.Int64("article-id", 0, "只重试该文章的失败投递")
	index := flags.String("index", "", "只重试该物理索引的失败投递")
	limit := flags.Int("limit", 0, "本次最多重新激活的投递数量（1..1000，必填）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("search retry 不接受位置参数\n%s", searchUsage)
	}
	scope := projectionApp.RetryScope{ArticleID: *articleID, Index: *index, Limit: *limit}
	// 命令自身也校验范围：管理操作必须始终带上明确上限。
	if !scope.Valid() {
		return fmt.Errorf("%w: limit 必须介于 1 和 %d\n%s", projectionApp.ErrInvalidRetryScope, projectionApp.MaxRetryLimit, searchUsage)
	}
	now := time.Now
	if r.options.Now != nil {
		now = r.options.Now
	}
	runAt := now().UTC()
	report, err := r.options.Search.Retry(ctx, scope, runAt)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout(), "失败投影重试报告\n")
	fmt.Fprintf(r.stdout(), "运行时刻: %s\n", runAt.Format(time.RFC3339))
	fmt.Fprintf(r.stdout(), "范围: article_id=%d index=%s limit=%d\n", scope.ArticleID, emptyAsAny(scope.Index), scope.Limit)
	fmt.Fprintf(r.stdout(), "范围内失败投递: %d（其中 generation 已推进、被跳过: %d）\n", report.FailedTotal, report.Superseded)
	fmt.Fprintf(r.stdout(), "本次重新激活: %d\n", len(report.Reactivated))
	fmt.Fprintf(r.stdout(), "范围内仍未完成: %d\n", report.Remaining)
	for _, item := range report.Reactivated {
		fmt.Fprintf(r.stdout(), "- article_id=%d index=%s projection_generation=%d\n", item.ArticleID, item.Index, item.Generation)
	}
	return nil
}

func emptyAsAny(value string) string {
	if value == "" {
		return "(全部)"
	}
	return value
}

func (r *Runner) runSearchIndex(ctx context.Context, args []string) error {
	if r.options.Search == nil {
		return errors.New("未配置 OpenSearch，无法初始化搜索索引")
	}
	if len(args) != 1 || args[0] != "init" {
		return fmt.Errorf("search index 只支持 init\n%s", searchUsage)
	}
	state, err := r.options.Search.InitIndex(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout(), "搜索索引已初始化\n")
	fmt.Fprintf(r.stdout(), "物理索引: %s\n", state.PhysicalIndex)
	fmt.Fprintf(r.stdout(), "schema 版本: %d\n", state.SchemaVersion)
	fmt.Fprintf(r.stdout(), "schema 身份: %s\n", state.SchemaIdentity)
	fmt.Fprintf(r.stdout(), "读别名: %t 写别名: %t\n", state.HasReadAlias, state.HasWriteAlias)
	fmt.Fprintf(r.stdout(), "可检索文档: %d\n", state.VisibleDocuments)
	return nil
}

func (r *Runner) runSearchRebuild(ctx context.Context, args []string) error {
	if r.options.Search == nil {
		return errors.New("未配置 OpenSearch，无法执行索引重建")
	}
	if len(args) == 0 {
		return fmt.Errorf("search rebuild 需要子命令\n%s", searchUsage)
	}
	id := ""
	if len(args) > 1 {
		id = args[1]
	}
	switch args[0] {
	case "start":
		state, err := r.options.Search.Start(ctx)
		if err != nil {
			return err
		}
		r.printRebuild(state)
		return nil
	case "resume":
		state, err := r.options.Search.Resume(ctx, id)
		if err != nil {
			return err
		}
		r.printRebuild(state)
		return nil
	case "status":
		flags := flag.NewFlagSet("search rebuild status", flag.ContinueOnError)
		flags.SetOutput(r.stderr())
		limit := flags.Int("limit", 5, "展示最近的重建记录数量")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		status, err := r.options.Search.Status(ctx, *limit)
		if err != nil {
			return err
		}
		r.printIndexStatus(status)
		return nil
	case "cutover":
		state, err := r.options.Search.Cutover(ctx, id)
		if err != nil {
			return err
		}
		r.printRebuild(state)
		return nil
	case "rollback":
		state, err := r.options.Search.Rollback(ctx, id)
		if err != nil {
			return err
		}
		r.printRebuild(state)
		return nil
	case "abandon":
		state, err := r.options.Search.Abandon(ctx, id)
		if err != nil {
			return err
		}
		r.printRebuild(state)
		return nil
	case "cleanup":
		flags := flag.NewFlagSet("search rebuild cleanup", flag.ContinueOnError)
		flags.SetOutput(r.stderr())
		index := flags.String("index", "", "要删除的精确物理索引名")
		confirm := flags.Bool("confirm", false, "确认删除该物理索引")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *index == "" {
			return fmt.Errorf("cleanup 必须指定 -index\n%s", searchUsage)
		}
		if err := r.options.Search.Cleanup(ctx, *index, *confirm); err != nil {
			return err
		}
		fmt.Fprintf(r.stdout(), "已删除物理索引 %s\n", *index)
		return nil
	default:
		return fmt.Errorf("不支持的 search rebuild 子命令 %q\n%s", args[0], searchUsage)
	}
}

func (r *Runner) printRebuild(state projectionApp.RebuildState) {
	fmt.Fprintf(r.stdout(), "重建 ID: %s\n", state.ID)
	fmt.Fprintf(r.stdout(), "候选索引: %s\n", state.CandidateIndex)
	fmt.Fprintf(r.stdout(), "阶段: %s\n", state.Phase)
	fmt.Fprintf(r.stdout(), "快照水位: article_id=%d 文档数=%d\n", state.SnapshotWatermark, state.SnapshotDocuments)
	fmt.Fprintf(r.stdout(), "增量水位: change_seq=%d（起点 %d）\n", state.CatchUpWatermark, state.StartChangeSeq)
	if state.Validation != nil {
		fmt.Fprintf(r.stdout(), "校验: 公开 %d 候选 %d 落后投递 %d 抽样不一致 %d 通过=%t\n",
			state.Validation.PublicDocuments, state.Validation.CandidateDocuments,
			state.Validation.LaggingDeliveries, len(state.Validation.Mismatches), state.Validation.Passed())
	}
	if state.RollbackDeadline != nil {
		fmt.Fprintf(r.stdout(), "回滚窗口截止: %s\n", state.RollbackDeadline.UTC().Format(time.RFC3339))
	}
	if state.LastError != "" {
		fmt.Fprintf(r.stdout(), "最近错误: %s\n", state.LastError)
	}
}

func (r *Runner) printIndexStatus(status projectionApp.IndexStatus) {
	if status.State.CurrentIndex == "" {
		fmt.Fprintf(r.stdout(), "搜索索引尚未初始化\n")
	} else {
		fmt.Fprintf(r.stdout(), "当前服务索引: %s（schema 版本 %d）\n", status.State.CurrentIndex, status.State.SchemaVersion)
		fmt.Fprintf(r.stdout(), "读别名: %s 写别名: %s\n", status.State.ReadAlias, status.State.WriteAlias)
		if status.State.RollbackIndex != "" {
			fmt.Fprintf(r.stdout(), "回滚索引: %s\n", status.State.RollbackIndex)
		}
	}
	if status.Active != nil {
		fmt.Fprintf(r.stdout(), "活动重建: %s 阶段 %s 候选 %s\n", status.Active.ID, status.Active.Phase, status.Active.CandidateIndex)
	} else {
		fmt.Fprintf(r.stdout(), "活动重建: 无\n")
	}
	for _, item := range status.History {
		fmt.Fprintf(r.stdout(), "- %s 阶段=%s 候选=%s 开始=%s\n", item.ID, item.Phase, item.CandidateIndex,
			item.StartedAt.UTC().Format(time.RFC3339))
	}
}
