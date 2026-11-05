# EasyCheck

移动签到 Go 客户端，支持登录、任务查询、定位签到和内置 cron 调度。需要 Go 1.24+。

## 下载可执行文件

GitHub Release 直接提供 Linux 和 Windows 的 amd64 可执行文件，无需解压或安装：

- Linux：`easycheck_<版本>_linux_amd64`，下载后执行 `chmod +x <文件名>`。
- Windows：`easycheck_<版本>_windows_amd64.exe`。

下文的 `go run ./cmd/easycheck` 可替换为下载的可执行文件路径。

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

从列表取得批次 ID（`pcid`），即可查看详情或提交签到，无需提供 `rwid`：

```sh
go run ./cmd/easycheck detail --pcid '<batch-id>'
go run ./cmd/easycheck sign --pcid '<batch-id>'
```

签到默认从任务允许地点生成坐标，再使用原站配置的腾讯地图自动解析位置名称，不再沿用固定地点名。自定义坐标时只需提供 `--longitude` 和 `--latitude`；显式提供 `--address` 可跳过地址解析。更多选项使用 `go run ./cmd/easycheck help sign` 查看。

## 定时签到

```sh
# 常驻运行，每天北京时间 08:00 自动查询并签到当前任务。
go run . sign --cron '0 8 * * *'

# 每 5 分钟检查一次。
go run . sign --cron '*/5 * * * *'
```

使用 `github.com/robfig/cron/v3` 在程序内调度，无需系统 crontab。表达式为五段：`分 时 日 月 星期`，默认时区 `Asia/Shanghai`，可用 `--timezone` 修改。程序持续运行，`Ctrl+C` 停止；已移除原来的 `--at`、`--after`。

导出 `EASYCHECK_USERNAME` 和 `EASYCHECK_PASSWORD` 后，每次定时触发先重新登录、更新会话文件中的 Cookie 和令牌，再翻页查询列表，为当前窗口内未签到的定位任务逐个复查并签到，无需提前登录或知道 `pcid`。启动及空闲等待时不登录；未配置账号密码时，兼容每轮读取已有会话的方式。跳过已签到、未开始、已结束和二维码任务；前一轮未完成时跳过重叠触发，本轮失败不终止调度。直接运行 `go run . sign` 则使用已有会话立即执行一轮后退出。详见 [CLI 使用说明](docs/CLI.md#定时签到)。

### 结果通知

登录和 cron 模式支持 Telegram 和飞书，可单独启用或同时启用。在 `.env` 中填写需要的渠道，未填写的渠道关闭：

```dotenv
EASYCHECK_TG_BOT_TOKEN='your-bot-token'
EASYCHECK_TG_CHAT_ID='your-chat-id'
EASYCHECK_FEISHU_WEBHOOK_URL='https://open.feishu.cn/open-apis/bot/v2/hook/your-webhook-id'
# 飞书机器人启用签名校验时填写，否则留空。
EASYCHECK_FEISHU_SECRET=''
EASYCHECK_NOTIFY_TIMEOUT=10s
```

登录失败后默认间隔 1 分钟重试，最多重试 3 次（首次尝试之外）；可通过 `EASYCHECK_LOGIN_RETRIES` 和 `EASYCHECK_LOGIN_RETRY_INTERVAL` 修改，设重试次数为 `0` 可关闭。配置渠道后每次登录失败都会通知。cron 每轮有签到成功或失败时，各渠道发送一次结果汇总；全部失败或部分成功都会通知，没有任务或仅跳过时不通知。通知失败只记录日志，不中断后续重试或调度，不重试签到。不带 `--cron` 的手动签到不发通知。

Compose 会传入这些变量；本机 CLI 需先导出到环境中。飞书签名等渠道配置见 [通知说明](docs/CLI.md#结果通知)。

## Docker

推送 `v*` 标签时，CI 将镜像发布到 `ghcr.io/nhirsama/easycheck`，镜像标签与 Git 标签一致（如 `v1.2.3`）。正式版本同时更新 `latest`，预发布版本不更新 `latest`。

```sh
git clone https://github.com/nhirsama/easycheck.git
cd easycheck
cp .env.example .env
# 编辑 .env，填写站点、账号、密码和 cron 表达式。
docker compose up -d
docker compose logs -f
```

`compose.yaml` 默认使用 GHCR 的 `latest` 镜像，也可以通过 `docker compose up -d --build` 从本地源码构建。容器启动后直接进入 cron 调度，每次触发先登录并保存新会话，再自动查找当前窗口内的任务，无需填写 `pcid`。登录失败后默认每隔 1 分钟重试 3 次；全部失败则跳过本轮，下次触发重新尝试。

Compose 自动读取 `.env`；`EASYCHECK_CRON` 默认是 `'*/5 * * * *'`（每 5 分钟），`EASYCHECK_TIMEZONE` 默认是 `Asia/Shanghai`。例如每天北京时间 08:00，设置 `EASYCHECK_CRON='0 8 * * *'`。修改配置后再次运行 `docker compose up -d` 应用。

**不挂载任何会话卷**：每轮登录将新会话写入容器内的 `/data/session.json`，删除或重建容器后丢弃，无需提前准备会话文件或因会话过期而重启。停止并移除容器使用 `docker compose down`。

定时自动登录要求站点未开启登录验证码；开启时本轮登录失败并按配置重试，可通过手动 `login` 输入验证码后使用未配置账号密码的会话模式。
