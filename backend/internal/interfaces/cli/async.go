package cli

import (
	"context"
	"flag"
	"fmt"

	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
)

func (r *Runner) runAI(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "backfill" {
		return fmt.Errorf("缺少或不支持的 ai 命令\n%s", usage)
	}
	if r.options.AIBackfill == nil {
		return fmt.Errorf("AI 补录未装配")
	}
	flags := flag.NewFlagSet("ai backfill", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	stage := flags.String("stage", "", "阶段：generation|embedding|all（必填）")
	mode := flags.String("mode", "", "模式：missing-only|outdated-only（必填）")
	articleID := flags.Int64("article-id", 0, "精确处理一篇文章")
	limit := flags.Int("limit", 0, "有限批次最多处理文章数（1..1000）")
	order := flags.String("order", "", "有限批次顺序：oldest|newest（默认 oldest）")
	all := flags.Bool("all", false, "处理全部符合条件的文章")
	confirmAll := flags.Bool("confirm-all", false, "确认全量操作可能产生大量模型费用")
	dryRun := flags.Bool("dry-run", false, "只预览，不写入")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *stage != "generation" && *stage != "embedding" && *stage != "all" {
		return fmt.Errorf("-stage 必须是 generation、embedding 或 all")
	}
	if *mode != "missing-only" && *mode != "outdated-only" {
		return fmt.Errorf("-mode 必须是 missing-only 或 outdated-only")
	}
	selectors := 0
	if *articleID > 0 {
		selectors++
	}
	if *limit > 0 {
		selectors++
	}
	if *all {
		selectors++
	}
	if selectors != 1 || *articleID < 0 || *limit < 0 {
		return fmt.Errorf("必须且只能提供 -article-id、-limit 或 -all 之一")
	}
	if *limit > 1000 {
		return fmt.Errorf("-limit 必须介于 1 和 1000")
	}
	if *order != "" && *limit == 0 {
		return fmt.Errorf("-order 只能与 -limit 一起使用")
	}
	if *order == "" && *limit > 0 {
		*order = "oldest"
	}
	if *order != "" && *order != "oldest" && *order != "newest" {
		return fmt.Errorf("-order 必须是 oldest 或 newest")
	}
	if *confirmAll && !*all {
		return fmt.Errorf("-confirm-all 只能与 -all 一起使用")
	}
	if *all && !*dryRun && !*confirmAll {
		return fmt.Errorf("全量补录可能产生大量模型费用，必须同时提供 -confirm-all")
	}
	report, err := r.options.AIBackfill.Run(ctx, enrichmentApp.BackfillRequest{Stage: *stage, Mode: *mode, ArticleID: *articleID, Limit: *limit,
		Order: *order, All: *all, ConfirmAll: *confirmAll, DryRun: *dryRun, GenerationProfile: r.options.AIGenerationProfile, EmbeddingProfile: r.options.AIEmbeddingProfile})
	fmt.Fprintf(r.stdout(), "created=%d skipped=%d failed=%d has-more=%t dry-run=%t\n", report.Created, report.Skipped, report.Failed, report.HasMore, *dryRun)
	return err
}

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
