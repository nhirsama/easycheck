# EasyCheck

移动签到 Go 客户端，支持登录、任务查询和定位签到。需要 Go 1.24+，仅依赖标准库。

## 最小使用

设置应用根地址、账号和密码，将示例值替换为实际配置：

```sh
export EASYCHECK_BASE_URL='https://example.invalid/qdgj/'
export EASYCHECK_USERNAME='example-user'
export EASYCHECK_PASSWORD='replace-with-password'
```

登录并列出签到任务：

```sh
go run ./cmd/easycheck login
go run ./cmd/easycheck list
```

登录成功后，会话保存到 `session.json`，后续命令自动使用。验证码开启时按提示输入。

从列表取得任务 ID（`rwid`）和批次 ID（`pcid`），查看详情或提交签到：

```sh
go run ./cmd/easycheck detail --rwid '<task-id>' --pcid '<batch-id>'
go run ./cmd/easycheck sign --rwid '<task-id>' --pcid '<batch-id>'
```

签到默认从任务允许地点生成定位。更多选项使用 `go run ./cmd/easycheck help sign` 查看。

- [CLI 使用说明](docs/CLI.md)
- [Go 包调用与协议参考](docs/API.md)
