package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// maxPasswordInputBytes 限制从 stdin 读取的密码长度，超出由密码规则拒绝。
const maxPasswordInputBytes = 4096

func (r *Runner) runAccount(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: velis-admin account <init-admin|set-role|set-status> [参数]")
	}
	switch args[0] {
	case "init-admin":
		return r.initAdmin(ctx, args[1:])
	case "set-role":
		return r.setRole(ctx, args[1:])
	case "set-status":
		return r.setStatus(ctx, args[1:])
	default:
		return fmt.Errorf("不支持的 account 命令 %q", args[0])
	}
}

func (r *Runner) initAdmin(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("account init-admin", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	username := flags.String("username", "", "管理员用户名")
	passwordStdin := flags.Bool("password-stdin", false, "从标准输入读取密码，仅用于自动化")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return errors.New("account init-admin 需要 -username")
	}
	password, err := r.readPassword(*passwordStdin, true)
	if err != nil {
		return err
	}
	operationID, err := accountDomain.NewUUID()
	if err != nil {
		return err
	}
	user, err := r.accounts().InitAdmin(ctx, accountApp.InitAdminInput{
		Username: *username, Password: password, OperationID: operationID.String(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout(), "username=%s role=%s status=%s created=true\n", user.Username, user.Role, user.Status)
	return nil
}

func (r *Runner) setRole(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("account set-role", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	username := flags.String("username", "", "目标用户名")
	role := flags.String("role", "", "user 或 admin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *username == "" || *role == "" {
		return errors.New("account set-role 需要 -username 与 -role")
	}
	operationID, err := accountDomain.NewUUID()
	if err != nil {
		return err
	}
	user, changed, err := r.accounts().SetRole(ctx, accountApp.SetRoleInput{
		Username: *username, Role: accountDomain.Role(*role), OperationID: operationID.String(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout(), "username=%s role=%s changed=%t\n", user.Username, user.Role, changed)
	if !changed {
		fmt.Fprintln(r.stderr(), "目标角色与现值相同，未做修改")
	}
	return nil
}

func (r *Runner) setStatus(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("account set-status", flag.ContinueOnError)
	flags.SetOutput(r.stderr())
	username := flags.String("username", "", "目标用户名")
	status := flags.String("status", "", "active 或 disabled")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *username == "" || *status == "" {
		return errors.New("account set-status 需要 -username 与 -status")
	}
	operationID, err := accountDomain.NewUUID()
	if err != nil {
		return err
	}
	user, changed, err := r.accounts().SetStatus(ctx, accountApp.SetStatusInput{
		Username: *username, Status: accountDomain.Status(*status), OperationID: operationID.String(),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout(), "username=%s status=%s changed=%t\n", user.Username, user.Status, changed)
	if !changed {
		fmt.Fprintln(r.stderr(), "目标状态与现值相同，未做修改")
	}
	return nil
}

// readPassword 读取密码：-password-stdin 时只移除一个结尾 LF 或 CRLF，否则走终端隐藏输入。
func (r *Runner) readPassword(fromStdin, confirm bool) (string, error) {
	if fromStdin {
		raw, err := io.ReadAll(io.LimitReader(r.options.Stdin, maxPasswordInputBytes))
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"), nil
	}
	if r.options.HiddenInput == nil {
		return "", errors.New("当前环境不支持隐藏输入，请使用 -password-stdin")
	}
	password, err := r.options.HiddenInput("请输入密码: ")
	if err != nil {
		return "", err
	}
	if !confirm {
		return password, nil
	}
	repeated, err := r.options.HiddenInput("请再次输入密码: ")
	if err != nil {
		return "", err
	}
	if password != repeated {
		return "", errors.New("两次输入的密码不一致")
	}
	return password, nil
}

func (r *Runner) accounts() AccountAdminService {
	if r.options.Accounts == nil {
		return unavailableAccounts{}
	}
	return r.options.Accounts
}

// unavailableAccounts 在未装配账户用例时给出明确错误，而不是空指针崩溃。
type unavailableAccounts struct{}

func (unavailableAccounts) InitAdmin(context.Context, accountApp.InitAdminInput) (accountDomain.User, error) {
	return accountDomain.User{}, errors.New("当前进程未装配账户维护用例")
}

func (unavailableAccounts) SetRole(context.Context, accountApp.SetRoleInput) (accountDomain.User, bool, error) {
	return accountDomain.User{}, false, errors.New("当前进程未装配账户维护用例")
}

func (unavailableAccounts) SetStatus(context.Context, accountApp.SetStatusInput) (accountDomain.User, bool, error) {
	return accountDomain.User{}, false, errors.New("当前进程未装配账户维护用例")
}
