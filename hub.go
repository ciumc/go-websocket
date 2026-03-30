package websocket

import (
	"log"
	"sync"
)

// Hub 维护活跃客户端集合并向客户端广播消息。
// 它作为 WebSocket 服务器的中央消息代理。
// 它可以安全地用于多个 goroutine 的并发访问。
type Hub struct {
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
}

// NewHub 创建一个新的 Hub 实例，初始化所有 channel 和 map。
// Hub 在调用 Run() 之前不会启动。
func NewHub() *Hub {
	h := &Hub{
		broadcast:   make(chan []byte, 100),
		register:    make(chan *Client, 100),
		unregister:  make(chan *Client, 100),
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
	h := NewHub()
	go h.Run()
	return h
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
		close(h.done)
	})
}

// Broadcast 向所有已连接的客户端发送消息。
// 消息被排队等待异步发送。
// 此方法立即返回，即使客户端无法接收消息。
func (h *Hub) Broadcast(message []byte) {
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
		func() {
			defer func() { recover() }()
			close(h.broadcast)
		}()
		func() {
			defer func() { recover() }()
			close(h.register)
		}()
		func() {
			defer func() { recover() }()
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
