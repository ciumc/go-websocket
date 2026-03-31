package websocket

import (
	"log"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// Hub 维护活跃客户端集合并向客户端广播消息。
// 它作为 WebSocket 服务器的中央消息代理。
// 它可以安全地用于多个 goroutine 的并发访问。
type Hub struct {
	// config 是 Hub 的配置
	config Config

	// upgrader 是 WebSocket 升级器（不再是全局变量）
	upgrader websocket.Upgrader

	// clientsLock 保护 clients map 免受并发访问
	clientsLock sync.RWMutex

	// clients 是以客户端 ID 为键的已注册客户端映射
	clients map[string]*Client

	// broadcast 是用于客户端入站消息广播的 channel
	broadcast chan []byte

	// register 是客户端注册请求的 channel
	register chan *Client

	// unregister 是客户端注销请求的 channel
	unregister chan *Client

	// done 是用于优雅关闭信号通知的 channel
	done chan struct{}

	// closeOnce 确保 done channel 只关闭一次
	closeOnce sync.Once

	// closed 标志 Hub 是否已关闭（用于 Broadcast 安全检查）
	closed atomic.Bool
}

// NewHub 创建一个新的 Hub 实例，初始化所有 channel 和 map。
// Hub 在调用 Run() 之前不会启动。
// 使用 DefaultConfig 作为默认配置。
func NewHub() *Hub {
	return NewHubWithConfig()
}

// NewHubWithConfig 使用配置选项创建一个新的 Hub 实例。
// 如果没有提供选项，使用 DefaultConfig。
func NewHubWithConfig(opts ...HubOption) *Hub {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(&config)
	}

	// 使用 config.BufSize 作为 channel 缓冲区大小
	bufSize := config.BufSize
	if bufSize <= 0 {
		bufSize = 256 // 默认缓冲区大小
	}

	h := &Hub{
		config: config,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     config.CheckOrigin,
		},
		broadcast:   make(chan []byte, bufSize),
		register:    make(chan *Client, bufSize),
		unregister:  make(chan *Client, bufSize),
		clients:     make(map[string]*Client),
		clientsLock: sync.RWMutex{},
		done:        make(chan struct{}),
		closeOnce:   sync.Once{},
	}
	return h
}

// NewHubRun 创建一个新的 Hub 并在单独的 goroutine 中启动它。
// 这是一个便利函数，结合了 NewHub() 和 Run()。
func NewHubRun() *Hub {
	return NewHubRunWithConfig()
}

// NewHubRunWithConfig 使用配置选项创建一个新的 Hub 并启动它。
func NewHubRunWithConfig(opts ...HubOption) *Hub {
	h := NewHubWithConfig(opts...)
	go h.Run()
	return h
}

// Config 返回 Hub 的配置副本。
func (h *Hub) Config() Config {
	return h.config
}

// Upgrader 返回 Hub 的 WebSocket 升级器。
func (h *Hub) Upgrader() *websocket.Upgrader {
	return &h.upgrader
}

// Client 根据 ID 获取客户端。
// 返回客户端和一个表示是否找到该客户端的布尔值。
// 此方法可安全地用于并发访问。
func (h *Hub) Client(id string) (*Client, bool) {
	h.clientsLock.RLock()
	defer h.clientsLock.RUnlock()
	cli, ok := h.clients[id]
	return cli, ok
}

// Close 通过关闭 done channel 来优雅地关闭 Hub。
// 这会向 Run() 循环发送信号以停止并清理资源。
// 使用 sync.Once 确保 channel 只关闭一次，避免重复关闭 panic。
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		h.closed.Store(true)
		close(h.done)
	})
}

// Broadcast 向所有已连接的客户端发送消息。
// 消息被排队等待异步发送。
// 此方法立即返回，即使客户端无法接收消息。
// 如果 Hub 已关闭，消息会被丢弃。
func (h *Hub) Broadcast(message []byte) {
	if h.closed.Load() {
		return
	}
	select {
	case h.broadcast <- message:
	case <-h.done:
		return
	}
}

// Run 启动 Hub 的主事件循环。
// 此函数处理客户端注册、注销和消息广播。
// 它应该在单独的 goroutine 中调用，或者使用 NewHubRun()。
// 循环会持续运行直到 Close() 被调用。
func (h *Hub) Run() {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("panic: %v", err)
		}

		// 关闭所有客户端连接
		h.clientsLock.Lock()
		for _, client := range h.clients {
			delete(h.clients, client.id)
			client.closeSend()
			if client.conn != nil {
				_ = client.conn.Close()
			}
		}
		h.clients = make(map[string]*Client)
		h.clientsLock.Unlock()

		// 安全地关闭所有 channel（使用 recover 防止重复关闭 panic）
		// 注意：重复关闭不应发生，如果发生则记录日志以便诊断
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("hub: broadcast channel double-close detected (should not happen): %v", r)
				}
			}()
			close(h.broadcast)
		}()
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("hub: register channel double-close detected (should not happen): %v", r)
				}
			}()
			close(h.register)
		}()
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("hub: unregister channel double-close detected (should not happen): %v", r)
				}
			}()
			close(h.unregister)
		}()
	}()

	for {
		select {
		case <-h.done:
			return
		case client := <-h.register:
			h.clientsLock.Lock()
			h.clients[client.id] = client
			h.clientsLock.Unlock()
		case client := <-h.unregister:
			h.clientsLock.Lock()
			if _, ok := h.clients[client.id]; ok {
				delete(h.clients, client.id)
				client.closeSend()
				if client.conn != nil {
					_ = client.conn.Close()
				}
			}
			h.clientsLock.Unlock()
		case message := <-h.broadcast:
			h.clientsLock.RLock()
			clients := make([]*Client, 0, len(h.clients))
			for _, client := range h.clients {
				clients = append(clients, client)
			}
			h.clientsLock.RUnlock()
			for _, client := range clients {
				select {
				case client.send <- message:
				default:
					// 如果客户端无法接收消息，可能已断开连接，移除它
					h.clientsLock.Lock()
					delete(h.clients, client.id)
					h.clientsLock.Unlock()
					client.closeSend()
					if client.conn != nil {
						_ = client.conn.Close()
					}
				}
			}
		}
	}
}
