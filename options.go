package websocket

import (
	"net/http"
	"time"
)

// HubOption 配置 Hub 的函数类型。
// 使用函数式选项模式（Functional Options Pattern），允许灵活配置 Hub。
//
// 使用示例:
//
//	hub := NewHubRunWithConfig(
//	    WithWriteWait(5*time.Second),
//	    WithPongWait(30*time.Second),
//	    WithPingPeriod(27*time.Second), // 必须小于 PongWait
//	    WithMaxMessageSize(1024),
//	    WithHubBufSize(512),
//	)
type HubOption func(c *Config)

// WithWriteWait 设置 WebSocket 写入超时时间。
// 默认值: 10 秒
//
// 此值决定了写入操作的最大等待时间。
// 如果网络延迟较高，可能需要增加此值。
// 警告: 过小的值可能导致频繁的写入超时错误。
func WithWriteWait(d time.Duration) HubOption {
	return func(c *Config) { c.WriteWait = d }
}

// WithPongWait 设置等待客户端 Pong 响应的超时时间。
// 默认值: 60 秒
//
// 此值决定了服务器等待客户端 Pong 响应的最大时间。
// 如果超过此时间未收到 Pong，连接将被关闭。
// 警告: PingPeriod 必须小于此值，否则连接会被错误关闭。
func WithPongWait(d time.Duration) HubOption {
	return func(c *Config) { c.PongWait = d }
}

// WithPingPeriod 设置发送 Ping 消息的周期。
// 默认值: 54 秒
//
// 此值决定了服务器向客户端发送心跳 Ping 的频率。
// 建议: 设置为 PongWait 的 90% 左右，留出网络延迟余量。
// 警告: 必须小于 PongWait，否则会在收到 Pong 前发送下一个 Ping。
func WithPingPeriod(d time.Duration) HubOption {
	return func(c *Config) { c.PingPeriod = d }
}

// WithMaxMessageSize 设置单个消息的最大字节数。
// 默认值: 512 字节
//
// 此值用于防止过大的消息消耗过多内存。
// 警告: 过小的值可能导致正常消息被拒绝。
func WithMaxMessageSize(size int64) HubOption {
	return func(c *Config) { c.MaxMessageSize = size }
}

// WithHubBufSize 设置 Hub 默认的发送缓冲区大小。
// 默认值: 256
//
// 此值决定了每个客户端的 send channel 缓冲区大小。
// 较大的值可以处理消息突发，但会消耗更多内存。
// 注意: 可通过 Session 级别的 WithSessionBufSize 覆盖此默认值。
func WithHubBufSize(size int) HubOption {
	return func(c *Config) { c.BufSize = size }
}

// WithHubCheckOrigin 设置 WebSocket 升级时的 CORS 检查函数。
// 默认: 允许所有来源（不安全，仅用于开发环境）
//
// 生产环境示例:
//
//	hub := NewHubRunWithConfig(
//	    WithHubCheckOrigin(func(r *http.Request) bool {
//	        origin := r.Header.Get("Origin")
//	        return origin == "https://example.com"
//	    }),
//	)
//
// 警告: 生产环境必须设置适当的 Origin 检查，否则存在安全风险。
func WithHubCheckOrigin(fn func(r *http.Request) bool) HubOption {
	return func(c *Config) { c.CheckOrigin = fn }
}
