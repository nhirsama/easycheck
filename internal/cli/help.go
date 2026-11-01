package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func (a *app) printRootHelp() error {
	var text strings.Builder
	fmt.Fprintln(&text, "EasyCheck — 移动签到客户端")
	fmt.Fprintln(&text, "\n用法：\n  easycheck <命令> [选项]\n  easycheck help [命令]")
	fmt.Fprintln(&text, "\n命令：")
	table := tabwriter.NewWriter(&text, 0, 4, 2, ' ', 0)
	for _, spec := range commands {
		fmt.Fprintf(table, "  %s\t%s\n", spec.name, spec.summary)
	}
	fmt.Fprintln(table, "  help\t查看命令帮助")
	table.Flush()
	fmt.Fprintln(&text, "\n公共选项（放在命令之后）：")
	printFlags(&text, a.newCommand(commandSpec{}), true)
	fmt.Fprintln(&text, "\n配置优先级：命令行参数 > 环境变量 > 默认值。")
	fmt.Fprintln(&text, "\n帮助选项：\n  -h, --help  查看帮助")
	fmt.Fprintln(&text, "\n示例：\n  easycheck login\n  easycheck list --page 1 --page-size 10\n  easycheck help sign")
	_, err := io.WriteString(a.output, text.String())
	return err
}

func (a *app) printCommandHelp(c *command) error {
	var text strings.Builder
	fmt.Fprintln(&text, c.spec.summary)
	fmt.Fprintf(&text, "\n用法：\n  %s %s\n", c.spec.path(), c.spec.syntax)
	if c.spec.description != "" {
		fmt.Fprintf(&text, "\n%s\n", c.spec.description)
	}
	if hasCommandFlags(c) {
		fmt.Fprintln(&text, "\n命令选项：")
		printFlags(&text, c, false)
	}
	fmt.Fprintln(&text, "\n公共选项：")
	printFlags(&text, c, true)
	fmt.Fprintln(&text, "\n配置优先级：命令行参数 > 环境变量 > 默认值。")
	fmt.Fprintln(&text, "\n帮助选项：\n  -h, --help  查看帮助")
	if len(c.spec.examples) > 0 {
		fmt.Fprintln(&text, "\n示例：")
		for _, example := range c.spec.examples {
			fmt.Fprintln(&text, "  "+example)
		}
	}
	_, err := io.WriteString(a.output, text.String())
	return err
}

func hasCommandFlags(c *command) bool {
	found := false
	c.flags.VisitAll(func(f *flag.Flag) {
		if !isCommonFlag(f.Name) {
			found = true
		}
	})
	return found
}

func printFlags(output io.Writer, c *command, common bool) {
	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	c.flags.VisitAll(func(f *flag.Flag) {
		if isCommonFlag(f.Name) != common {
			return
		}
		name, description := flag.UnquoteUsage(f)
		option := "--" + f.Name
		if name != "" {
			option += " " + name
		}
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			description += "（默认 " + f.DefValue + "）"
		}
		fmt.Fprintf(table, "  %s\t%s\n", option, description)
	})
	table.Flush()
}
