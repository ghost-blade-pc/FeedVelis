package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type cliAccountService struct {
	initInput   accountApp.InitAdminInput
	roleInput   accountApp.SetRoleInput
	statusInput accountApp.SetStatusInput
	roleChanged bool
	statusChang bool
	err         error
}

func (s *cliAccountService) InitAdmin(_ context.Context, input accountApp.InitAdminInput) (accountDomain.User, error) {
	s.initInput = input
	if s.err != nil {
		return accountDomain.User{}, s.err
	}
	return accountDomain.User{Username: "root", Role: accountDomain.RoleAdmin, Status: accountDomain.StatusActive}, nil
}

func (s *cliAccountService) SetRole(_ context.Context, input accountApp.SetRoleInput) (accountDomain.User, bool, error) {
	s.roleInput = input
	if s.err != nil {
		return accountDomain.User{}, false, s.err
	}
	return accountDomain.User{Username: "alice", Role: input.Role, Status: accountDomain.StatusActive}, s.roleChanged, nil
}

func (s *cliAccountService) SetStatus(_ context.Context, input accountApp.SetStatusInput) (accountDomain.User, bool, error) {
	s.statusInput = input
	if s.err != nil {
		return accountDomain.User{}, false, s.err
	}
	return accountDomain.User{Username: "alice", Role: accountDomain.RoleUser, Status: input.Status}, s.statusChang, nil
}

func TestInitAdminFromStdinStripsOneTrailingNewline(t *testing.T) {
	cases := map[string]string{
		"Abcd123!\n":   "Abcd123!",
		"Abcd123!\r\n": "Abcd123!",
		"Abcd123!":     "Abcd123!",
		"Abcd123!\n\n": "Abcd123!\n",
	}
	for input, want := range cases {
		service := &cliAccountService{}
		var stdout bytes.Buffer
		runner := New(Options{Accounts: service, Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &bytes.Buffer{}})
		if err := runner.Run(context.Background(), []string{"account", "init-admin", "-username", "Root", "-password-stdin"}); err != nil {
			t.Fatalf("执行失败: %v", err)
		}
		if service.initInput.Password != want {
			t.Fatalf("stdin %q 解析为 %q，期望 %q", input, service.initInput.Password, want)
		}
		if service.initInput.Username != "Root" {
			t.Fatalf("用户名 = %q", service.initInput.Username)
		}
		if _, err := accountDomain.ParseUUID(service.initInput.OperationID); err != nil {
			t.Fatalf("操作 ID 必须是 UUID: %v", err)
		}
		if !strings.Contains(stdout.String(), "role=admin") {
			t.Fatalf("输出 = %q", stdout.String())
		}
	}
}

func TestInitAdminUsesHiddenInputWithConfirmation(t *testing.T) {
	service := &cliAccountService{}
	var prompts []string
	answers := []string{"Abcd123!", "Abcd123!"}
	runner := New(Options{
		Accounts: service,
		Stdin:    strings.NewReader(""),
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
		HiddenInput: func(prompt string) (string, error) {
			prompts = append(prompts, prompt)
			return answers[len(prompts)-1], nil
		},
	})
	if err := runner.Run(context.Background(), []string{"account", "init-admin", "-username", "root"}); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || service.initInput.Password != "Abcd123!" {
		t.Fatalf("隐藏输入 = %v，密码 = %q", prompts, service.initInput.Password)
	}
}

func TestInitAdminRejectsMismatchedConfirmation(t *testing.T) {
	answers := []string{"Abcd123!", "Abcd124!"}
	index := 0
	runner := New(Options{
		Accounts:    &cliAccountService{},
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
		HiddenInput: func(string) (string, error) { index++; return answers[index-1], nil },
	})
	err := runner.Run(context.Background(), []string{"account", "init-admin", "-username", "root"})
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("两次输入不一致时必须拒绝: %v", err)
	}
}

func TestInitAdminRequiresExplicitPasswordSource(t *testing.T) {
	runner := New(Options{Accounts: &cliAccountService{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	err := runner.Run(context.Background(), []string{"account", "init-admin", "-username", "root"})
	if err == nil || !strings.Contains(err.Error(), "-password-stdin") {
		t.Fatalf("无隐藏输入时必须提示使用 -password-stdin: %v", err)
	}
	if err := runner.Run(context.Background(), []string{"account", "init-admin"}); err == nil {
		t.Fatal("缺少 -username 必须报错")
	}
}

func TestSetRoleAndSetStatusOutput(t *testing.T) {
	service := &cliAccountService{roleChanged: true}
	var stdout, stderr bytes.Buffer
	runner := New(Options{Accounts: service, Stdout: &stdout, Stderr: &stderr})
	if err := runner.Run(context.Background(), []string{"account", "set-role", "-username", "alice", "-role", "admin"}); err != nil {
		t.Fatal(err)
	}
	if service.roleInput.Role != accountDomain.RoleAdmin || !strings.Contains(stdout.String(), "changed=true") {
		t.Fatalf("输入 = %+v 输出 = %q", service.roleInput, stdout.String())
	}

	service.roleChanged = false
	stdout.Reset()
	if err := runner.Run(context.Background(), []string{"account", "set-role", "-username", "alice", "-role", "admin"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "changed=false") || !strings.Contains(stderr.String(), "未做修改") {
		t.Fatalf("同值变更应幂等提示: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	service.statusChang = true
	stdout.Reset()
	if err := runner.Run(context.Background(), []string{"account", "set-status", "-username", "alice", "-status", "disabled"}); err != nil {
		t.Fatal(err)
	}
	if service.statusInput.Status != accountDomain.StatusDisabled || !strings.Contains(stdout.String(), "changed=true") {
		t.Fatalf("输入 = %+v 输出 = %q", service.statusInput, stdout.String())
	}
}

func TestAccountCommandErrors(t *testing.T) {
	service := &cliAccountService{err: accountDomain.ErrLastAdmin}
	cases := [][]string{
		{"account"},
		{"account", "unknown"},
		{"account", "set-role", "-username", "alice"},
		{"account", "set-status", "-status", "disabled"},
		{"group", "list"},
		{},
	}
	for _, args := range cases {
		options := Options{Accounts: &cliAccountService{}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
		if err := New(options).Run(context.Background(), args); err == nil {
			t.Fatalf("参数 %v 必须报错", args)
		}
	}

	options := Options{Accounts: service, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := New(options).Run(context.Background(), []string{"account", "set-status", "-username", "root", "-status", "disabled"}); !errors.Is(err, accountDomain.ErrLastAdmin) {
		t.Fatalf("用例错误必须向上返回: %v", err)
	}
	if err := New(Options{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}).Run(context.Background(),
		[]string{"account", "set-role", "-username", "alice", "-role", "admin"}); err == nil || !strings.Contains(err.Error(), "未装配") {
		t.Fatalf("未装配用例时必须明确报错: %v", err)
	}
}
