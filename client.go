package websocket

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	// newline 表示换行符的字节形式。
	newline = []byte{'\n'}

	// space 表示空格字符的字节形式。
	space = []byte{' '}
)

// upgrader 保留用于向后兼容，但新代码应使用 Hub.Upgrader()
// Deprecated: 此全局变量将在未来版本中移除，请使用 Hub 的 Upgrader() 方法。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Client 表示一个 WebSocket 客户端连接。
// 它管理连接生命周期、消息发送/接收和事件回调。
// Client 的方法可以安全地用于并发调用。
type Client struct {
	// hub 管理所有活跃客户端并广播消息
	hub *Hub

	// conn 是底层的 WebSocket 连接
	conn *websocket.Conn

	// send 是用于出站消息的缓冲 channel
	send chan []byte

	// id 唯一标识客户端
	id string

	// closeOnce 确保 send channel 只关闭一次
	closeOnce sync.Once

	// closed 标识客户端是否已关闭（用于 Emit 安全检查）
	closed atomic.Bool

	// 用于处理连接事件的事件回调
	onEvent      func(conn *Client, messageType int, message []byte)
	onConnect    func(conn *Client)
	onError      func(id string, err error)
	onDisconnect func(id string)

	// config 是配置覆盖（可选，用于 Session）
	config *Config

	// bufSize 是 send channel 的缓冲区大小（在创建 channel 前使用）
	bufSize int
}

// effectiveConfig 获取有效配置。
// 如果 Client 有配置覆盖，使用覆盖的配置；
// 否则使用 Hub 的配置。
func (c *Client) effectiveConfig() Config {
	if c.config != nil {
		return *c.config
	}
	return c.hub.Config()
}

// Emit 向客户端发送消息。
// 如果客户端已关闭或发送缓冲区已满，则返回 false。
// 此方法可安全地用于并发使用，即使 channel 被并发关闭也不会 panic。
func (c *Client) Emit(message []byte) bool {
	// 先检查是否已关闭，快速路径
	if c.closed.Load() {
		return false
	}
	// 使用 defer recover 来处理可能的竞态条件
	// 即使在检查后 channel 被并发关闭，也不会 panic
	defer func() {
		if r := recover(); r != nil {
			// channel 已被关闭，忽略 panic
		}
	}()
	select {
	case c.send <- message:
		return true
	default:
		// 发送缓冲区已满，返回失败
		return false
	}
}

// closeSend 安全地关闭 send channel，使用 sync.Once 防止重复关闭。
// 设置 closed 标志以防止后续 Emit 调用向已关闭的 channel 发送。
func (c *Client) closeSend() {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.send)
	})
}

// Broadcast 通过 hub 向所有已连接客户端发送消息。
// 消息将被排队等待传递给所有客户端。
func (c *Client) Broadcast(message []byte) {
	c.hub.Broadcast(message)
}

// GetID 返回此客户端的唯一标识符。
// ID 在创建客户端时生成，并在连接生命周期内保持不变。
func (c *Client) GetID() string {
	return c.id
}

// reader 循环从 WebSocket 连接读取消息。
// 它处理 pong 消息并处理传入的消息。
// 如果发生任何错误，它会从 hub 注销客户端。
func (c *Client) reader() {
	defer func() {
		c.hub.unregister <- c
		if err := recover(); err != nil {
			c.error(fmt.Errorf("panic: %v", err))
		}
	}()

	cfg := c.effectiveConfig()

	// 设置消息大小限制
	c.conn.SetReadLimit(cfg.MaxMessageSize)

	// 设置初始读取超时
	if err := c.conn.SetReadDeadline(time.Now().Add(cfg.PongWait)); err != nil {
		c.error(err)
		return
	}

	// 设置 pong 处理器
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(cfg.PongWait))
	})

	for {
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
				c.error(err)
			}
			return
		}
		if c.onEvent != nil {
			// 规范化消息：将换行符替换为空格并去除首尾空白
			message = bytes.TrimSpace(bytes.ReplaceAll(message, newline, space))
			c.onEvent(c, messageType, message)
		}
	}
}

// writer 处理向 WebSocket 连接写入消息。
// 它从 send channel 发送消息并定期 ping 客户端。
// 它确保在 writer 停止时进行适当的清理。
func (c *Client) writer() {
	cfg := c.effectiveConfig()
	ticker := time.NewTicker(cfg.PingPeriod)
	defer func() {
		ticker.Stop()
		if c.onDisconnect != nil {
			c.onDisconnect(c.id)
		}
		if err := recover(); err != nil {
			c.error(fmt.Errorf("panic: %v", err))
		}
	}()

	for {
		select {
		case message, ok := <-c.send:
			// 设置写入超时
			if err := c.setWriteDeadline(); err != nil {
				c.error(err)
				return
			}

			if !ok {
				// channel 已关闭，发送关闭消息
				if err := c.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil && !errors.Is(err, websocket.ErrCloseSent) {
					c.error(err)
				}
				return
			}

			// 获取 writer
			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				c.error(err)
				return
			}

			// 写入当前消息
			if _, err := w.Write(message); err != nil {
				c.error(err)
				return
			}

			// 写入队列中的其他消息
			n := len(c.send)
			for i := 0; i < n; i++ {
				if _, err := w.Write(newline); err != nil {
					c.error(err)
					return
				}
				msg := <-c.send
				if _, err := w.Write(msg); err != nil {
					c.error(err)
					return
				}
			}

			// 关闭 writer
			if err := w.Close(); err != nil {
				c.error(err)
				return
			}

		case <-ticker.C:
			// 发送 ping 消息
			if err := c.setWriteDeadline(); err != nil {
				c.error(err)
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.error(err)
				return
			}
		}
	}
}

// setWriteDeadline 设置写入截止时间。
func (c *Client) setWriteDeadline() error {
	return c.conn.SetWriteDeadline(time.Now().Add(c.effectiveConfig().WriteWait))
}

// Option 是一个配置 Client 的函数。
// 它遵循函数式选项模式以实现灵活的客户端配置。
type Option func(c *Client)

// WithID 设置客户端 ID。
// 如果未提供，将自动生成 UUID。
func WithID(id string) Option {
	return func(c *Client) {
		c.id = id
	}
}

// WithBufSize 设置 send channel 的缓冲区大小。
// 此选项在 NewClient 中应用，channel 在所有选项处理后才创建，
// 因此不会出现竞态条件。
func WithBufSize(size int) Option {
	return func(c *Client) {
		if size > 0 {
			c.bufSize = size
		}
	}
}

// WithCheckOrigin 设置 WebSocket upgrader 的 CheckOrigin 函数。
// 用于自定义跨域检查逻辑。默认允许所有来源（return true）。
//
// Deprecated: 请使用 Hub 的 WithHubCheckOrigin 选项。
// 此函数修改全局 upgrader 变量，可能导致并发测试中的竞态条件。
// 迁移方式：使用 NewHubWithConfig(WithHubCheckOrigin(fn)) 替代。
func WithCheckOrigin(fn func(r *http.Request) bool) Option {
	return func(c *Client) {
		// 修改全局 upgrader 的 CheckOrigin
		// 注意：此操作有线程安全问题，仅保留用于向后兼容
		upgrader.CheckOrigin = fn
	}
}

// NewClient 使用给定的 hub 和选项创建一个新的 Client 实例。
// 如果未通过选项提供 ID，将生成 UUID。
// 客户端在调用 Conn() 之前不会连接。
//
// 选项在 channel 创建前应用，确保 WithBufSize 等选项不会导致竞态条件。
func NewClient(hub *Hub, opts ...Option) *Client {
	cfg := hub.Config()
	cli := &Client{
		hub: hub,
	}

	// 先应用所有选项
	for _, opt := range opts {
		opt(cli)
	}

	// 生成 ID（如果未设置）
	if cli.id == "" {
		cli.id = strings.ReplaceAll(uuid.NewString(), "-", "")
	}

	// 确定缓冲区大小：选项 > Hub 配置
	bufSize := cfg.BufSize
	if cli.bufSize > 0 {
		bufSize = cli.bufSize
	}

	// 最后创建 channel，避免竞态条件
	cli.send = make(chan []byte, bufSize)

	return cli
}

// Conn 将 HTTP 连接升级为 WebSocket 连接并启动客户端。
// 它处理：
// - 将 HTTP 连接升级为 WebSocket
// - 向 hub 注册客户端
// - 启动 reader 和 writer goroutine
// - 触发 OnConnect 回调
//
// 参数：
// - w: HTTP 响应写入器
// - r: HTTP 请求
//
// 如果连接升级失败则返回错误。
func (c *Client) Conn(w http.ResponseWriter, r *http.Request) error {
	// 将 HTTP 连接升级为 WebSocket（使用 Hub 的 upgrader）
	conn, err := c.hub.Upgrader().Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	// 设置连接并注册客户端
	c.conn = conn
	c.hub.register <- c

	// 启动 reader 和 writer goroutine
	go c.writer()
	go c.reader()

	// 触发连接回调
	if c.onConnect != nil {
		c.onConnect(c)
	}

	return nil
}

// error 通过调用错误回调或记录来处理客户端的错误。
func (c *Client) error(err error) {
	if c.onError != nil {
		c.onError(c.id, err)
	} else {
		log.Printf("error: %s %v", c.id, err)
		debug.PrintStack()
	}
}

// OnEvent 设置用于处理传入 WebSocket 消息的回调。
// 回调接收：
// - conn: 接收消息的客户端
// - messageType: WebSocket 消息类型（文本/二进制）
// - message: 消息负载
func (c *Client) OnEvent(handler func(conn *Client, messageType int, message []byte)) {
	c.onEvent = handler
}

// OnConnect 设置 WebSocket 连接建立时的回调。
// 回调接收：
// - conn: 新连接的客户端
func (c *Client) OnConnect(handler func(conn *Client)) {
	c.onConnect = handler
}

// OnError 设置处理 WebSocket 错误的回调。
// 回调接收：
// - id: 发生错误的客户端 ID
// - err: 发生的错误
func (c *Client) OnError(handler func(id string, err error)) {
	c.onError = handler
}

// OnDisconnect 设置 WebSocket 连接关闭时的回调。
// 回调接收：
// - id: 断开连接的客户端 ID
func (c *Client) OnDisconnect(handler func(id string)) {
	c.onDisconnect = handler
}
