// Package qdgj 提供通用的移动签到协议客户端。
//
// 使用 NewClient(Config) 一次配置地址、账号密码及验证码回调，然后通过
// 同一个对象的 Login、LoadSession、SaveSession、ListTasks 等方法操作。
// ConfigFromEnv 集中加载环境变量，客户端保存创建时的配置副本。
// Login 从入口页面及登录脚本自动获取 RSA 公钥。
//
// 登录、任务查询和签到使用客户端独立的 CookieJar，网络操作接受 context.Context。
// Login 执行登录，SignIn 提交签到，ListTasks 和 GetTask 查询任务。
// 完成登录或会话加载后可以并发查询任务。
package qdgj
