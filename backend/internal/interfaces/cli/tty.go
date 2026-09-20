package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// TTYPasswordPrompt 返回从终端隐藏读取一行的实现；标准输入不是终端时直接报错，
// 提示调用方改用 -password-stdin。
func TTYPasswordPrompt(promptWriter io.Writer) func(string) (string, error) {
	if promptWriter == nil {
		promptWriter = io.Discard
	}
	return func(prompt string) (string, error) {
		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			return "", errors.New("标准输入不是终端，请使用 -password-stdin")
		}
		fmt.Fprint(promptWriter, prompt)
		value, err := term.ReadPassword(fd)
		fmt.Fprintln(promptWriter)
		if err != nil {
			return "", err
		}
		return string(value), nil
	}
}
