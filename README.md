# EasyCheck

移动签到 Go 客户端，支持登录、任务查询和定位签到。需要 Go 1.24+，仅依赖标准库。

## 最小使用

通过命令行参数登录，将示例值替换为实际配置：

```sh
go run ./cmd/easycheck login \
  --base-url 'https://example.invalid/qdgj/' \
  --username 'example-user' --password '<password>'
```

配置优先级：**命令行参数 > 环境变量 > 默认值**。`--base-url`、`--username`、`--password`、`--session` 分别对应 `EASYCHECK_BASE_URL`、`EASYCHECK_USERNAME`、`EASYCHECK_PASSWORD`、`EASYCHECK_SESSION_FILE`。

查询任务时传入相同的基础地址，或设置 `EASYCHECK_BASE_URL`：

```sh
export EASYCHECK_BASE_URL='https://example.invalid/qdgj/'
go run ./cmd/easycheck list
```

登录成功后，会话保存到 `session.json`，后续命令自动使用。验证码开启时按提示输入。

从列表取得任务 ID（`rwid`）和批次 ID（`pcid`），查看详情或提交签到：

```sh
go run ./cmd/easycheck detail --rwid '<task-id>' --pcid '<batch-id>'
go run ./cmd/easycheck sign --rwid '<task-id>' --pcid '<batch-id>'
```

签到默认从任务允许地点生成定位。更多选项使用 `go run ./cmd/easycheck help sign` 查看。

## Docker

```sh
docker build -t easycheck .
docker run --rm -it \
  -e EASYCHECK_BASE_URL='https://example.invalid/qdgj/' \
  -e EASYCHECK_USERNAME='example-user' \
  -e EASYCHECK_PASSWORD='replace-with-password' \
  -v easycheck-data:/data \
  easycheck login
```

后续用同一个 `/data` 卷运行 `list`、`detail` 或 `sign`，即可复用登录会话。
