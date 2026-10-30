// Package qdgj 提供通用的移动签到协议客户端。
//
// 登录、任务查询和签到均使用客户端独立的 CookieJar。所有网络操作接受
// context.Context；客户端不自动重试，也不跟随重定向。只有显式调用 SignIn
// 才会提交签到，登录及查询不会触发它。完成登录后可以并发查询任务。
package qdgj
