package websocket

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jayecc/go-websocket/websocketpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// DistServer 实现分布式 WebSocket 操作的 gRPC 服务端。
// 它处理来自集群中其他节点的请求，对本地连接的客户端执行操作。
type DistServer struct {
	// hub 是包含连接到本服务器实例的客户端的本地 hub
	hub *Hub
}

// NewDistServer 使用给定的 hub 创建一个新的 DistServer 实例。
func NewDistServer(hub *Hub) *DistServer {
	return &DistServer{
		hub: hub,
	}
}

// Emit 通过 gRPC 向特定客户端发送消息。
// 它通过 ID 查找客户端，如果客户端存在则发送消息。
// 返回成功状态表示消息是否已发送。
func (c *DistServer) Emit(_ context.Context, request *websocketpb.EmitRequest) (response *websocketpb.EmitResponse, err error) {
	response = &websocketpb.EmitResponse{}
	client, ok := c.hub.Client(request.GetId())
	if !ok {
		response.Success = false
		return
	}
	response.Success = client.Emit(request.GetData())
	return
}

// Online 通过 gRPC 检查客户端是否在线。
// 它通过 ID 查找客户端并返回其是否存在。
func (c *DistServer) Online(_ context.Context, request *websocketpb.OnlineRequest) (response *websocketpb.OnlineResponse, err error) {
	response = &websocketpb.OnlineResponse{}
	_, ok := c.hub.Client(request.Id)
	response.Online = ok
	return
}

// Broadcast 通过 gRPC 向所有客户端广播消息。
// 它向所有本地连接的客户端广播消息。
func (c *DistServer) Broadcast(_ context.Context, request *websocketpb.BroadcastRequest) (response *websocketpb.BroadcastResponse, err error) {
	response = &websocketpb.BroadcastResponse{}
	c.hub.Broadcast(request.Data)
	response.Count = 1
	return
}

// DistSession 表示一个分布式 WebSocket 会话。
// 它包装了一个 Client 并与 Storage 集成以跟踪客户端位置。
type DistSession struct {
	// client 是底层的 WebSocket 客户端
	client *Client

	// storage 用于存储和检索客户端位置信息
	storage Storage

	// addr 是本服务器节点的地址
	addr string
}

// NewDistSession 使用给定的 hub、storage 和地址创建一个新的 DistSession 实例。
// 可以提供额外的客户端选项。
func NewDistSession(hub *Hub, storage Storage, addr string, opts ...Option) *DistSession {
	return &DistSession{
		client:  NewClient(hub, opts...),
		storage: storage,
		addr:    addr,
	}
}

// Client 返回内部的 Client 实例。
func (c *DistSession) Client() *Client {
	return c.client
}

// OnEvent 设置处理传入 WebSocket 消息的回调。
// 这是对 client.OnEvent 方法的包装。
func (c *DistSession) OnEvent(handler func(conn *Client, messageType int, message []byte)) {
	c.client.OnEvent(func(conn *Client, messageType int, message []byte) {
		handler(conn, messageType, message)
	})
}

// OnConnect 设置 WebSocket 连接建立时的回调。
// 它将客户端位置存储到 storage 中，然后调用提供的处理程序。
func (c *DistSession) OnConnect(handler func(conn *Client)) {
	c.client.OnConnect(func(conn *Client) {
		if err := c.storage.Set(conn.GetID(), c.addr); err != nil {
			c.client.error(err)
		}
		handler(conn)
	})
}

// OnError 设置处理 WebSocket 错误的回调。
// 这是对 client.OnError 方法的包装。
func (c *DistSession) OnError(handler func(id string, err error)) {
	c.client.OnError(handler)
}

// OnDisconnect 设置 WebSocket 连接关闭时的回调。
// 它从 storage 中移除客户端，然后调用提供的处理程序。
func (c *DistSession) OnDisconnect(handler func(id string)) {
	c.client.OnDisconnect(func(id string) {
		if err := c.storage.Del(id); err != nil {
			c.client.error(err)
		}
		if handler != nil {
			handler(id)
		}
	})
}

// Conn 将 HTTP 连接升级为 WebSocket 连接并启动会话。
// 这是对 client.Conn 方法的包装。
func (c *DistSession) Conn(w http.ResponseWriter, r *http.Request) error {
	return c.client.Conn(w, r)
}

// DistClient 表示用于分布式 WebSocket 操作的客户端。
// 它被一个服务器节点用于与其他节点上连接的客户端通信。
type DistClient struct {
	// storage 用于查找客户端连接的位置
	storage Storage

	// timeout 是 gRPC 调用的超时时间，默认为 2 秒
	timeout time.Duration
}

// DistClientOption 是配置 DistClient 的函数。
type DistClientOption func(c *DistClient)

// WithDistTimeout 设置 DistClient 的超时时间。
func WithDistTimeout(timeout time.Duration) DistClientOption {
	return func(c *DistClient) {
		c.timeout = timeout
	}
}

// NewDistClient 使用给定的 storage 和选项创建一个新的 DistClient 实例。
func NewDistClient(storage Storage, opts ...DistClientOption) *DistClient {
	cli := &DistClient{
		storage: storage,
	}
	for _, opt := range opts {
		opt(cli)
	}
	return cli
}

// getTimeout 返回 gRPC 调用的超时时间。
// 如果未设置，则返回默认的 2 秒。
func (c *DistClient) getTimeout() time.Duration {
	if c.timeout == 0 {
		return 2 * time.Second
	}
	return c.timeout
}

// pooledConn 表示连接池中的一个连接，包含连接本身和最后使用时间。
type pooledConn struct {
	conn     *grpc.ClientConn
	lastUsed int64 // UnixNano 时间戳
}

// grpcClientPool 存储到其他服务器节点的 gRPC 客户端连接。
// 它使用 sync.Map 进行线程安全的并发访问。
var grpcClientPool sync.Map

// poolCleanupOnce 确保清理 goroutine 只启动一次。
var poolCleanupOnce sync.Once

// defaultConnTTL 是连接在池中的默认存活时间。
const defaultConnTTL = 5 * time.Minute

// startPoolCleanup 启动一个后台 goroutine 定期清理过期的连接。
func startPoolCleanup() {
	poolCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				cleanupExpiredConnections()
			}
		}()
	})
}

// cleanupExpiredConnections 清理池中过期的连接。
func cleanupExpiredConnections() {
	now := time.Now().UnixNano()
	threshold := defaultConnTTL.Nanoseconds()
	grpcClientPool.Range(func(key, value interface{}) bool {
		pc := value.(*pooledConn)
		if now-atomic.LoadInt64(&pc.lastUsed) > threshold {
			grpcClientPool.Delete(key)
			_ = pc.conn.Close()
		}
		return true
	})
}

// CloseGrpcPool 优雅地关闭连接池中的所有连接。
func CloseGrpcPool() {
	grpcClientPool.Range(func(key, value interface{}) bool {
		grpcClientPool.Delete(key)
		if pc, ok := value.(*pooledConn); ok {
			_ = pc.conn.Close()
		} else if conn, ok := value.(*grpc.ClientConn); ok {
			// 兼容旧版本直接存储的连接
			_ = conn.Close()
		}
		return true
	})
}

// grpcClientConn 获取或创建到指定地址的 gRPC 客户端连接。
// 它管理连接重用和清理，替换过期的连接。
func grpcClientConn(addr string) (*grpc.ClientConn, error) {
	// 确保清理 goroutine 已启动
	startPoolCleanup()

	instance, ok := grpcClientPool.Load(addr)
	if ok {
		pc := instance.(*pooledConn)
		switch pc.conn.GetState() {
		case connectivity.Idle, connectivity.Ready, connectivity.Connecting:
			atomic.StoreInt64(&pc.lastUsed, time.Now().UnixNano())
			return pc.conn, nil
		default:
			grpcClientPool.Delete(addr)
			_ = pc.conn.Close()
		}
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	pc := &pooledConn{
		conn:     conn,
		lastUsed: time.Now().UnixNano(),
	}
	grpcClientPool.Store(addr, pc)
	return conn, nil
}

// Emit 向分布式系统中的特定客户端发送消息。
// 它查找客户端的位置，连接到该节点，并发送消息。
// 返回消息是否成功发送。
func (c *DistClient) Emit(ctx context.Context, id string, message []byte) (ok bool, err error) {
	addr, err := c.storage.Get(id)
	if err != nil {
		return
	}
	if addr == "" {
		err = fmt.Errorf("%s not found", id)
		return
	}

	// 使用超时创建上下文
	timeout := c.getTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpcClientConn(addr)
	if err != nil {
		return
	}
	response, err := websocketpb.NewWebsocketClient(conn).Emit(ctx, &websocketpb.EmitRequest{
		Id:   id,
		Data: message,
	})
	if err != nil {
		return
	}
	return response.Success, nil
}

// Online 检查分布式系统中的客户端是否在线。
// 它查找客户端的位置并询问该节点客户端是否已连接。
// 返回客户端是否在线。
func (c *DistClient) Online(ctx context.Context, id string) (ok bool, err error) {
	addr, err := c.storage.Get(id)
	if err != nil {
		return
	}
	if addr == "" {
		err = fmt.Errorf("%s not found", id)
		return
	}

	// 使用超时创建上下文
	timeout := c.getTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpcClientConn(addr)
	if err != nil {
		return
	}
	response, err := websocketpb.NewWebsocketClient(conn).Online(ctx, &websocketpb.OnlineRequest{Id: id})
	if err != nil {
		return
	}
	return response.Online, nil
}

// Broadcast 向分布式系统中的所有客户端广播消息。
// 它检索所有客户端位置并向每个节点发送消息。
// 返回成功发送的数量。
// 注意：此方法使用 BroadcastV2 的实现（按地址去重），这是更优的实现。
func (c *DistClient) Broadcast(ctx context.Context, message []byte) (count int64, err error) {
	addrs, err := c.storage.All()
	if err != nil {
		return
	}

	// 使用 map 去重，避免向同一地址多次发送
	uniqueAddrs := make(map[string]struct{})
	for _, addr := range addrs {
		uniqueAddrs[addr] = struct{}{}
	}

	// 使用超时创建上下文
	timeout := c.getTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 遍历所有唯一地址并广播消息
	for addr := range uniqueAddrs {
		conn, err := grpcClientConn(addr)
		if err != nil {
			continue
		}

		response, err := websocketpb.NewWebsocketClient(conn).Broadcast(ctx, &websocketpb.BroadcastRequest{
			Data: message,
		})
		if err != nil {
			continue
		}

		count += response.Count
	}

	return
}

// BroadcastV2 已弃用，请使用 Broadcast。
// 保留此方法是为了向后兼容，它直接调用 Broadcast。
// Deprecated: 使用 Broadcast 代替。
func (c *DistClient) BroadcastV2(ctx context.Context, message []byte) (count int64, err error) {
	return c.Broadcast(ctx, message)
}
