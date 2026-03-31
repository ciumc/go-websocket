package websocket

import (
	"net/http"
	"time"
)

// HubOption 配置 Hub（修改 Config）
type HubOption func(c *Config)

// WithWriteWait 设置写入超时
func WithWriteWait(d time.Duration) HubOption {
	return func(c *Config) { c.WriteWait = d }
}

// WithPongWait 设置 Pong 等待超时
func WithPongWait(d time.Duration) HubOption {
	return func(c *Config) { c.PongWait = d }
}

// WithPingPeriod 设置 Ping 发送周期
func WithPingPeriod(d time.Duration) HubOption {
	return func(c *Config) { c.PingPeriod = d }
}

// WithMaxMessageSize 设置最大消息大小
func WithMaxMessageSize(size int64) HubOption {
	return func(c *Config) { c.MaxMessageSize = size }
}

// WithHubBufSize 设置 Hub 默认发送缓冲区大小
func WithHubBufSize(size int) HubOption {
	return func(c *Config) { c.BufSize = size }
}

// WithHubCheckOrigin 设置 Hub 的 CORS 检查函数
func WithHubCheckOrigin(fn func(r *http.Request) bool) HubOption {
	return func(c *Config) { c.CheckOrigin = fn }
}
