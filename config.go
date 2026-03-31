package websocket

import (
	"fmt"
	"net/http"
	"time"
)

// Config WebSocket 服务配置。
// 所有字段都有合理的默认值，可通过 HubOption 或 SessionOption 进行覆盖。
//
// 配置优先级: Session 级别配置 > Hub 级别配置 > 默认值
type Config struct {
	// WriteWait 写入超时时间。
	// 控制每次 WebSocket 写入操作的最大等待时间。
	// 设计理由: 防止写入操作无限期阻塞，及时释放资源。
	// 默认值: 10 秒
	WriteWait time.Duration

	// PongWait Pong 响应等待超时。
	// 控制服务器等待客户端 Pong 响应的最大时间。
	// 设计理由: 检测客户端是否存活，及时清理断开的连接。
	// 约束: 必须大于 PingPeriod，否则会在收到 Pong 前关闭连接。
	// 默认值: 60 秒
	PongWait time.Duration

	// PingPeriod Ping 消息发送周期。
	// 控制服务器向客户端发送心跳 Ping 的频率。
	// 设计理由: 保持连接活跃，检测客户端是否响应。
	// 约束: 必须小于 PongWait（建议为 PongWait 的 90%）。
	// 默认值: 54 秒（PongWait 的 90%）
	PingPeriod time.Duration

	// MaxMessageSize 单条消息最大字节数。
	// 控制服务器接受的单条消息大小上限。
	// 设计理由: 防止恶意客户端发送超大消息消耗服务器内存。
	// 默认值: 512 字节
	MaxMessageSize int64

	// BufSize 发送缓冲区大小。
	// 控制 send channel 的缓冲区容量。
	// 设计理由: 缓冲消息突发，避免发送方阻塞。
	// 较大的值可处理消息高峰，但会消耗更多内存。
	// 默认值: 256
	BufSize int

	// CheckOrigin CORS 检查函数。
	// 在 WebSocket 升级时验证请求来源。
	// 设计理由: 防止跨站 WebSocket 劫持攻击。
	// 默认值: 允许所有来源（不安全，仅限开发环境）
	// 生产环境: 必须设置为验证特定来源的函数
	CheckOrigin func(r *http.Request) bool
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
