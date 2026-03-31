package websocket

import (
	"fmt"
	"net/http"
	"time"
)

// Config WebSocket 服务配置
type Config struct {
	// 超时配置
	WriteWait      time.Duration // 写入超时，默认 10s
	PongWait       time.Duration // Pong 等待超时，默认 60s
	PingPeriod     time.Duration // Ping 发送周期，默认 54s
	MaxMessageSize int64         // 最大消息大小，默认 512 bytes

	// 缓冲区配置
	BufSize int // 发送缓冲区大小，默认 256

	// WebSocket 升级器配置
	CheckOrigin func(r *http.Request) bool // CORS 检查，默认允许所有
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		WriteWait:      10 * time.Second,
		PongWait:       60 * time.Second,
		PingPeriod:     54 * time.Second,
		MaxMessageSize: 512,
		BufSize:        256,
		CheckOrigin:    func(r *http.Request) bool { return true },
	}
}

// Validate 验证配置是否有效。
// 返回配置错误的描述，如果配置有效则返回 nil。
func (c Config) Validate() error {
	if c.WriteWait <= 0 {
		return fmt.Errorf("write wait must be positive, got %v", c.WriteWait)
	}
	if c.PongWait <= 0 {
		return fmt.Errorf("pong wait must be positive, got %v", c.PongWait)
	}
	if c.PingPeriod <= 0 {
		return fmt.Errorf("ping period must be positive, got %v", c.PingPeriod)
	}
	if c.PingPeriod >= c.PongWait {
		return fmt.Errorf("ping period (%v) must be less than pong wait (%v)", c.PingPeriod, c.PongWait)
	}
	if c.MaxMessageSize <= 0 {
		return fmt.Errorf("max message size must be positive, got %d", c.MaxMessageSize)
	}
	if c.BufSize < 0 {
		return fmt.Errorf("buffer size must be non-negative, got %d", c.BufSize)
	}
	return nil
}
