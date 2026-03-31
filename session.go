package websocket

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Session 统一的 WebSocket 会话入口。
// 自动检测单节点/分布式模式：
// - 无 storage: 单节点模式
// - 有 storage: 分布式模式
type Session struct {
	hub    *Hub
	client *Client

	// 分布式模式专用（单节点模式为 nil）
	storage Storage
	addr    string

	// 客户端 ID
	id string

	// 配置（从 Hub 继承，可覆盖）
	config Config
}

// NewSession 创建会话（自动检测模式）。
// 单节点模式：hub + opts
// 分布式模式：hub + WithStorage(storage) + WithAddr(addr) + opts
//
// 注意：分布式模式下必须同时提供 storage 和 addr，否则会引发 panic。
// 这是为了确保配置完整性，避免运行时错误。
func NewSession(hub *Hub, opts ...SessionOption) *Session {
	s := &Session{
		hub:    hub,
		config: hub.Config(), // 继承 Hub 配置
	}

	for _, opt := range opts {
		opt(s)
	}

	// 验证分布式模式配置完整性
	if s.storage != nil && s.addr == "" {
		panic("distributed mode requires addr: use WithAddr() to set the node address")
	}
	if s.storage == nil && s.addr != "" {
		panic("addr provided without storage: use WithStorage() to enable distributed mode")
	}

	// 生成 ID（如果未设置）
	if s.id == "" {
		s.id = strings.ReplaceAll(uuid.NewString(), "-", "")
	}

	// 创建内部 Client
	s.client = newClient(hub, s.id, s.config.BufSize)

	return s
}

// newClient 内部函数，创建 Client
// 使用 bufSize 字段确保 channel 在正确时机创建
func newClient(hub *Hub, id string, bufSize int) *Client {
	if bufSize <= 0 {
		bufSize = hub.Config().BufSize
	}
	return &Client{
		hub:     hub,
		id:      id,
		bufSize: bufSize,
		send:    make(chan []byte, bufSize),
	}
}

// Conn 建立 WebSocket 连接。
// 在分布式模式下（storage != nil），存储操作失败是非阻塞的：
// - 存储位置信息失败会记录错误但不阻止连接建立
// - 这是设计决策：连接的稳定性优先于存储的可靠性
func (s *Session) Conn(w http.ResponseWriter, r *http.Request) error {
	conn, err := s.hub.Upgrader().Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("websocket upgrade: %w", err)
	}

	s.client.conn = conn
	s.hub.register <- s.client

	go s.client.writer()
	go s.client.reader()

	// 触发连接回调
	if s.client.onConnect != nil {
		// 分布式模式：存储位置信息以便跨节点消息路由
		// 注意：存储失败会通过 OnError 回调报告，但不阻止连接
		if s.storage != nil {
			if err := s.storage.Set(s.client.id, s.addr); err != nil {
				s.client.error(err)
				// 继续执行：连接已建立，存储失败不影响本地通信
			}
		}
		s.client.onConnect(s.client)
	}

	return nil
}

// OnEvent 设置消息回调。
func (s *Session) OnEvent(handler func(conn *Client, messageType int, message []byte)) {
	s.client.onEvent = handler
}

// OnConnect 设置连接回调。
func (s *Session) OnConnect(handler func(conn *Client)) {
	s.client.onConnect = handler
}

// OnError 设置错误回调。
func (s *Session) OnError(handler func(id string, err error)) {
	s.client.onError = handler
}

// OnDisconnect 设置断开回调。
// 分布式模式会自动清理存储中的条目。
func (s *Session) OnDisconnect(handler func(id string)) {
	s.client.onDisconnect = func(id string) {
		if s.storage != nil {
			if err := s.storage.Del(id); err != nil {
				s.client.error(err)
			}
		}
		if handler != nil {
			handler(id)
		}
	}
}

// ID 返回客户端 ID。
func (s *Session) ID() string {
	return s.client.id
}

// Emit 发送消息。
func (s *Session) Emit(message []byte) bool {
	return s.client.Emit(message)
}

// Broadcast 广播消息到所有客户端。
func (s *Session) Broadcast(message []byte) {
	s.hub.Broadcast(message)
}

// Client 返回内部 Client（高级用法）。
func (s *Session) Client() *Client {
	return s.client
}

// SessionOption 配置 Session。
type SessionOption func(s *Session)

// WithSessionID 设置客户端 ID。
func WithSessionID(id string) SessionOption {
	return func(s *Session) { s.id = id }
}

// WithSessionBufSize 设置缓冲区大小。
func WithSessionBufSize(size int) SessionOption {
	return func(s *Session) { s.config.BufSize = size }
}

// WithSessionWriteWait 覆盖写入超时。
func WithSessionWriteWait(d time.Duration) SessionOption {
	return func(s *Session) { s.config.WriteWait = d }
}

// WithSessionPongWait 覆盖 Pong 等待超时。
func WithSessionPongWait(d time.Duration) SessionOption {
	return func(s *Session) { s.config.PongWait = d }
}

// WithSessionPingPeriod 覆盖 Ping 周期。
func WithSessionPingPeriod(d time.Duration) SessionOption {
	return func(s *Session) { s.config.PingPeriod = d }
}

// WithSessionMaxMessageSize 覆盖最大消息大小。
func WithSessionMaxMessageSize(size int64) SessionOption {
	return func(s *Session) { s.config.MaxMessageSize = size }
}

// WithStorage 设置存储后端（启用分布式模式）。
func WithStorage(storage Storage) SessionOption {
	return func(s *Session) { s.storage = storage }
}

// WithAddr 设置本节点地址（分布式模式必需）。
func WithAddr(addr string) SessionOption {
	return func(s *Session) { s.addr = addr }
}
