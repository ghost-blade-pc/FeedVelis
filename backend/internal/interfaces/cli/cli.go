// Package cli 提供仅限本地执行的管理协议适配器：Source 管理与账户维护。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

const usage = `用法: velis-admin <source|account> <命令> [参数]
  source add -url <feed-url>
  source list
  source pause|resume|fetch <id> [--force]
  account init-admin -username <name> [-password-stdin]
  account set-role -username <name> -role user|admin
  account set-status -username <name> -status active|disabled`

type SourceService interface {
	Add(context.Context, string) (sourceDomain.Source, bool, error)
	List(context.Context) ([]sourceDomain.Source, error)
	Pause(context.Context, int64) error
	Resume(context.Context, int64) error
	FetchByID(context.Context, int64, bool) (sourceApp.FetchOutcome, error)
}

// AccountAdminService 是本地账户维护用例；不依赖认证运行时配置。
type AccountAdminService interface {
	InitAdmin(context.Context, accountApp.InitAdminInput) (accountDomain.User, error)
	SetRole(context.Context, accountApp.SetRoleInput) (accountDomain.User, bool, error)
	SetStatus(context.Context, accountApp.SetStatusInput) (accountDomain.User, bool, error)
}

// Options 是 CLI 运行器的装配选项。
type Options struct {
	Sources  SourceService
	Accounts AccountAdminService
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	// HiddenInput 从终端隐藏读取一行输入；为 nil 时只能使用 -password-stdin。
	HiddenInput func(prompt string) (string, error)
}

type Runner struct{ options Options }

func New(options Options) *Runner {
	if options.Stdin == nil {
		options.Stdin = emptyReader{}
	}
	return &Runner{options: options}
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func (r *Runner) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "source":
		return r.runSource(ctx, args[1:])
	case "account":
		return r.runAccount(ctx, args[1:])
	default:
		return fmt.Errorf("不支持的命令组 %q\n%s", args[0], usage)
	}
}

func (r *Runner) stdout() io.Writer {
	if r.options.Stdout == nil {
		return io.Discard
	}
	return r.options.Stdout
}

func (r *Runner) stderr() io.Writer {
	if r.options.Stderr == nil {
		return io.Discard
	}
	return r.options.Stderr
}
