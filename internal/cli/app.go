// Package cli 实现 EasyCheck 的命令分发、参数解析和终端交互。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"easycheck/qdgj"
)

type app struct {
	input       io.Reader
	output      io.Writer
	diagnostics io.Writer
}

type commandSpec struct {
	program     string
	name        string
	summary     string
	syntax      string
	description string
	examples    []string
	setup       func(*app, *command)
}

func (s commandSpec) path() string {
	program := s.program
	if program == "" {
		program = "easycheck"
	}
	return strings.TrimSpace(program + " " + s.name)
}

type command struct {
	spec        commandSpec
	flags       *flag.FlagSet
	hiddenFlags map[string]bool
	config      qdgj.Config
	validate    func() error
	run         func(context.Context) error
}

var commands = []commandSpec{loginSpec, checkSpec, listSpec, detailSpec, signSpec}

// Run 解析命令并通过指定的输入输出执行操作。
func Run(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) error {
	a := &app{input: input, output: output, diagnostics: diagnostics}
	return a.run(ctx, args)
}

// Execute 执行命令，将错误写入诊断流并返回进程退出码。
func Execute(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) int {
	return exitCode(Run(ctx, args, input, output, diagnostics), diagnostics)
}

type usageError struct {
	err  error
	path string
}

func (e *usageError) Error() string {
	return fmt.Sprintf("%s；使用 %s --help 查看用法", e.err, e.path)
}

func (e *usageError) Unwrap() error { return e.err }

func exitCode(err error, diagnostics io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(diagnostics, "错误：", err)
	var usage *usageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.printRootHelp()
	}
	if args[0] == "help" {
		switch len(args) {
		case 1:
			return a.printRootHelp()
		case 2:
			for _, spec := range commands {
				if args[1] == spec.name {
					return a.printCommandHelp(a.newCommand(spec))
				}
			}
		default:
			return &usageError{err: errors.New("用法：easycheck help [命令]"), path: "easycheck"}
		}
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "-help" {
		if len(args) != 1 {
			return &usageError{err: errors.New("根帮助选项须单独使用"), path: "easycheck"}
		}
		return a.printRootHelp()
	}
	for _, spec := range commands {
		if args[0] == spec.name {
			return a.runCommand(ctx, spec, args[1:])
		}
	}
	if strings.HasPrefix(args[0], "-") {
		return a.runCommand(ctx, loginSpec, args)
	}
	name := args[0]
	if name == "help" {
		name = args[1]
	}
	return &usageError{err: fmt.Errorf("未知命令 %q", name), path: "easycheck"}
}

func (a *app) newCommand(spec commandSpec) *command {
	c := &command{spec: spec, config: qdgj.ConfigFromEnv()}
	c.flags = flag.NewFlagSet(spec.path(), flag.ContinueOnError)
	c.flags.SetOutput(io.Discard)
	c.flags.Usage = func() {}
	c.bindCommonFlags()
	if spec.setup != nil {
		spec.setup(a, c)
	}
	return c
}

func (a *app) runCommand(ctx context.Context, spec commandSpec, args []string) error {
	c := a.newCommand(spec)
	if err := c.flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return a.printCommandHelp(c)
		}
		return &usageError{err: err, path: spec.path()}
	}
	if c.flags.NArg() != 0 {
		return &usageError{err: errors.New("参数须使用选项名称"), path: spec.path()}
	}
	if c.config.Timeout <= 0 {
		return &usageError{err: errors.New("--timeout 必须大于零"), path: spec.path()}
	}
	if c.validate != nil {
		if err := c.validate(); err != nil {
			return &usageError{err: err, path: spec.path()}
		}
	}
	return c.run(ctx)
}

func (c *command) sessionClient() (*qdgj.Client, error) {
	client, err := qdgj.NewClient(c.config)
	if err != nil {
		return nil, err
	}
	if err := client.LoadSession(); err != nil {
		return nil, fmt.Errorf("加载会话：%w；请先执行 easycheck login", err)
	}
	return client, nil
}
