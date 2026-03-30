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
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// writeWait 是向对等方写入消息的时间限制。
	writeWait = 10 * time.Second

	// pongWait 是从对等方读取下一个 pong 消息的时间限制。
	pongWait = 60 * time.Second

	// pingPeriod 是向对等方发送 ping 的周期。必须小于 pongWait。
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize 是允许从对等方接收的最大消息大小。
	maxMessageSize = 512

	// bufSize 是出站消息的发送缓冲区大小。
	bufSize = 256
)

var (
	// newline 表示换行符的字节形式。
	newline = []byte{'\n'}

	// space 表示空格字符的字节形式。
	space = []byte{' '}
)

// upgrader 用于将 HTTP 连接升级为 WebSocket 连接。
// 它设置缓冲区大小并允许所有来源的 CORS。
// 注意：可以使用 WithCheckOrigin 选项自定义跨域检查逻辑。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Client 表示一个 WebSocket 客户端连接。
// 它管理连接生命周期、消息发送/接收和事件回调。
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

	// 用于处理连接事件的事件回调
	onEvent      func(conn *Client, messageType int, message []byte)
	onConnect    func(conn *Client)
	onError      func(id string, err error)
	onDisconnect func(id string)
}

// Emit 向客户端发送消息。
// 如果客户端已关闭或发送缓冲区已满，则返回 false。
// 此方法可安全地用于并发使用。
func (c *Client) Emit(message []byte) bool {
	select {
	case c.send <- message:
		return true
	default:
		// 发送缓冲区已满，返回失败
		return false
	}
}

// closeSend 安全地关闭 send channel，使用 sync.Once 防止重复关闭。
func (c *Client) closeSend() {
	c.closeOnce.Do(func() {
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

	// 设置消息大小限制
	c.conn.SetReadLimit(maxMessageSize)

	// 设置初始读取超时
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		c.error(err)
		return
	}

	// 设置 pong 处理器
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
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
			message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
			c.onEvent(c, messageType, message)
		}
	}
}

// writer 处理向 WebSocket 连接写入消息。
// 它从 send channel 发送消息并定期 ping 客户端。
// 它确保在 writer 停止时进行适当的清理。
func (c *Client) writer() {
	ticker := time.NewTicker(pingPeriod)
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
	return c.conn.SetWriteDeadline(time.Now().Add(writeWait))
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
// 必须在 NewClient 之前调用，因为 channel 在创建后无法更改大小。
func WithBufSize(size int) Option {
	return func(c *Client) {
		// 注意：这里只是标记，实际的 channel 创建在 NewClient 中处理
		// 由于 channel 已经创建，我们需要重新创建它
		if size > 0 {
			c.send = make(chan []byte, size)
		}
	}
}

// WithCheckOrigin 设置 WebSocket upgrader 的 CheckOrigin 函数。
// 用于自定义跨域检查逻辑。默认允许所有来源（return true）。
func WithCheckOrigin(fn func(r *http.Request) bool) Option {
	return func(c *Client) {
		// 修改全局 upgrader 的 CheckOrigin
		upgrader.CheckOrigin = fn
	}
}

// NewClient 使用给定的 hub 和选项创建一个新的 Client 实例。
// 如果未通过选项提供 ID，将生成 UUID。
// 客户端在调用 Conn() 之前不会连接。
func NewClient(hub *Hub, opts ...Option) *Client {
	cli := &Client{
		hub:  hub,
		send: make(chan []byte, bufSize),
	}

	for _, opt := range opts {
		opt(cli)
	}

	if cli.id == "" {
		cli.id = strings.ReplaceAll(uuid.NewString(), "-", "")
	}

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
	// 将 HTTP 连接升级为 WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
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
