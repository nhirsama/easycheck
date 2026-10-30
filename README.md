# EasyCheck

通用的移动签到协议 Go 客户端，提供登录、验证码、会话持久化、任务列表、批次详情和显式定位签到。需要 Go 1.24+，仅依赖标准库。

客户端包为 `easycheck/qdgj`。根目录是登录命令，`cmd/checkin-inspect` 是只读任务查询命令。

## 配置

站点基础 URL 从 `EASYCHECK_BASE_URL` 读取，没有内置目标地址。填写应用根地址，并保留部署路径，例如：

```sh
export EASYCHECK_BASE_URL='https://example.invalid/qdgj/'
```

上面的域名是占位词，须替换为实际部署地址。包内所有接口、Origin 和 Referer 均由同一基础 URL 构造。`-base-url` 或 `Options.BaseURL` 可显式覆盖环境变量。

登录还需要服务端对应的 Base64 DER/SPKI RSA 公钥：

```sh
export EASYCHECK_PUBLIC_KEY='REPLACE_WITH_BASE64_SPKI_PUBLIC_KEY'
export EASYCHECK_USERNAME='example-user'
```

`.env.example` 列出了配置项。程序不会自动加载 `.env`；如需使用文件，可复制为 `.env`、填写本地值，然后手动导出：

```sh
set -a
. ./.env
set +a
```

`.env`、会话、验证码和原始抓取目录均已加入忽略规则。

## 命令行

仅检查入口与验证码开关，不提交登录：

```sh
go run . -check
```

以 zsh 隐藏输入密码，再提交一次登录：

```zsh
read -rs 'EASYCHECK_PASSWORD?密码：'
echo
export EASYCHECK_PASSWORD
go run .
unset EASYCHECK_PASSWORD
```

也可使用 `-username`、`-password-stdin`、`-session`、`-captcha-file`、`-timeout` 等选项，详见 `go run . -help`。验证码开启时，命令保存图片并等待用户输入，在同一 Cookie 会话内提交登录。

登录成功后以 `0600` 权限保存 `session.json`，不保存明文密码。会话仅适用于保存时的站点，恢复时要求当前配置的基础 URL 与之相同。

查询签到任务和指定批次：

```sh
go run ./cmd/checkin-inspect
go run ./cmd/checkin-inspect -rwid '<task-id>' -pcid '<batch-id>'
go run ./cmd/checkin-inspect -out tasks-session.json
```

查询命令只调用 `queryQdrw`，不提交签到。签到提交仅由包的显式 `SignIn` 方法提供。

## 包调用与协议

- [PACKAGE.md](PACKAGE.md)：外部项目导入、客户端 API、验证码回调及错误处理。
- [CHECKIN_API.md](CHECKIN_API.md)：使用占位值的接口协议说明。

登录加密按兼容协议执行 RSA PKCS#1 v1.5 和前端摘要编码，不修改调用方的全局 `GODEBUG`。站点公钥由配置提供，仓库不包含任何真实部署的公钥或会话。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试仅使用本地模拟服务、占位数据和合成公钥，不访问实际部署，不提交真实签到。
