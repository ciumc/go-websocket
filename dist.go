package websocket

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ciumc/go-websocket/websocketpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// 连接池配置常量
const (
	// connPoolTTL 连接池中连接的生存时间，超过此时间的连接会被清理
	connPoolTTL = 5 * time.Minute
	// connPoolCleanupInterval 连接池清理检查间隔
	connPoolCleanupInterval = 1 * time.Minute
)

// DistServer 实现分布式 WebSocket 操作的 gRPC 服务端。
// 它处理来自集群中其他节点的请求，对本地连接的客户端执行操作。
type DistServer struct {
	hub *Hub
}

// NewDistServer 使用给定的 hub 创建一个新的 DistServer 实例。
func NewDistServer(hub *Hub) *DistServer {
	return &DistServer{
		hub: hub,
	}
}

// Emit 通过 gRPC 向特定客户端发送消息。
func (s *DistServer) Emit(_ context.Context, request *websocketpb.EmitRequest) (response *websocketpb.EmitResponse, err error) {
	response = &websocketpb.EmitResponse{}
	client, ok := s.hub.Client(request.GetId())
	if !ok {
		response.Success = false
		return
	}
	response.Success = client.Emit(request.GetData())
	return
}

// Online 通过 gRPC 检查客户端是否在线。
func (s *DistServer) Online(_ context.Context, request *websocketpb.OnlineRequest) (response *websocketpb.OnlineResponse, err error) {
	response = &websocketpb.OnlineResponse{}
	_, ok := s.hub.Client(request.Id)
	response.Online = ok
	return
}

// Broadcast 通过 gRPC 向所有客户端广播消息。
func (s *DistServer) Broadcast(_ context.Context, request *websocketpb.BroadcastRequest) (response *websocketpb.BroadcastResponse, err error) {
	response = &websocketpb.BroadcastResponse{}
	s.hub.Broadcast(request.Data)
	response.Count = 1
	return
}

// grpcPool 连接池（DistClient 内部使用）
type grpcPool struct {
	mu      sync.RWMutex
	conns   map[string]*pooledConn
	cleanup *time.Ticker
	done    chan struct{}
}

type pooledConn struct {
	conn     *grpc.ClientConn
	lastUsed int64
}

// newGrpcPool 创建连接池并启动清理 goroutine
func newGrpcPool() *grpcPool {
	p := &grpcPool{
		conns:   make(map[string]*pooledConn),
		cleanup: time.NewTicker(connPoolCleanupInterval),
		done:    make(chan struct{}),
	}
	go p.runCleanup()
	return p
}

// runCleanup 定期清理过期的连接。
// 使用 defer 确保 ticker 总是被停止，避免资源泄漏。
func (p *grpcPool) runCleanup() {
	defer p.cleanup.Stop() // 确保 ticker 总是被停止
	for {
		select {
		case <-p.cleanup.C:
			p.cleanExpired()
		case <-p.done:
			return
		}
	}
}

func (p *grpcPool) cleanExpired() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now().UnixNano()
	ttl := int64(connPoolTTL)

	for addr, pc := range p.conns {
		if now-atomic.LoadInt64(&pc.lastUsed) > ttl {
			if pc.conn != nil {
				_ = pc.conn.Close()
			}
			delete(p.conns, addr)
		}
	}
}

func (p *grpcPool) get(addr string) (*grpc.ClientConn, error) {
	p.mu.RLock()
	pc, ok := p.conns[addr]
	p.mu.RUnlock()

	if ok && pc.conn != nil {
		switch pc.conn.GetState() {
		case connectivity.Idle, connectivity.Ready, connectivity.Connecting:
			atomic.StoreInt64(&pc.lastUsed, time.Now().UnixNano())
			return pc.conn, nil
		default:
			p.mu.Lock()
			delete(p.conns, addr)
			_ = pc.conn.Close()
			p.mu.Unlock()
		}
	}

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.conns[addr] = &pooledConn{
		conn:     conn,
		lastUsed: time.Now().UnixNano(),
	}
	p.mu.Unlock()

	return conn, nil
}

func (p *grpcPool) close() {
	select {
	case <-p.done:
		// 已经关闭
		return
	default:
		close(p.done)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pc := range p.conns {
		if pc.conn != nil {
			_ = pc.conn.Close()
		}
	}
	p.conns = make(map[string]*pooledConn)
}

// DistClient 用于跨节点发送消息。
type DistClient struct {
	storage Storage
	pool    *grpcPool
	timeout time.Duration
}

// NewDistClient 创建分布式客户端。
func NewDistClient(storage Storage, opts ...DistClientOption) *DistClient {
	dc := &DistClient{
		storage: storage,
		pool:    newGrpcPool(),
	}
	for _, opt := range opts {
		opt(dc)
	}
	return dc
}

// Close 关闭分布式客户端，清理连接池。
func (dc *DistClient) Close() {
	if dc.pool != nil {
		dc.pool.close()
	}
}

func (dc *DistClient) getTimeout() time.Duration {
	if dc.timeout == 0 {
		return 2 * time.Second
	}
	return dc.timeout
}

// Emit 向指定客户端发送消息。
// 先检查父 context 是否已取消，避免无意义的存储查询和 gRPC 调用。
func (dc *DistClient) Emit(ctx context.Context, id string, message []byte) (bool, error) {
	// 先检查 context 是否已取消，快速失败
	if ctx.Err() != nil {
		return false, fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	addr, err := dc.storage.Get(id)
	if err != nil {
		return false, fmt.Errorf("storage get: %w", err)
	}
	if addr == "" {
		return false, fmt.Errorf("client %s not found", id)
	}

	ctx, cancel := context.WithTimeout(ctx, dc.getTimeout())
	defer cancel()

	conn, err := dc.pool.get(addr)
	if err != nil {
		return false, fmt.Errorf("grpc connect: %w", err)
	}

	client := websocketpb.NewWebsocketClient(conn)
	resp, err := client.Emit(ctx, &websocketpb.EmitRequest{
		Id:   id,
		Data: message,
	})
	if err != nil {
		return false, fmt.Errorf("grpc emit: %w", err)
	}
	return resp.Success, nil
}

// Online 检查客户端是否在线。
// 通过存储查询客户端所在节点，然后向该节点发送在线检查请求。
// 先检查父 context 是否已取消，避免无意义的存储查询和 gRPC 调用。
func (dc *DistClient) Online(ctx context.Context, id string) (bool, error) {
	// 先检查 context 是否已取消，快速失败
	if ctx.Err() != nil {
		return false, fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	addr, err := dc.storage.Get(id)
	if err != nil {
		return false, fmt.Errorf("storage get: %w", err)
	}
	if addr == "" {
		return false, fmt.Errorf("client %s not found", id)
	}

	ctx, cancel := context.WithTimeout(ctx, dc.getTimeout())
	defer cancel()

	conn, err := dc.pool.get(addr)
	if err != nil {
		return false, fmt.Errorf("grpc connect: %w", err)
	}

	client := websocketpb.NewWebsocketClient(conn)
	resp, err := client.Online(ctx, &websocketpb.OnlineRequest{Id: id})
	if err != nil {
		return false, fmt.Errorf("grpc online: %w", err)
	}
	return resp.Online, nil
}

// Broadcast 广播消息到所有节点。
// 首先从存储获取所有节点地址，去重后向每个节点发送广播请求。
// 先检查父 context 是否已取消，避免无意义的存储查询和 gRPC 调用。
func (dc *DistClient) Broadcast(ctx context.Context, message []byte) (int64, error) {
	// 先检查 context 是否已取消，快速失败
	if ctx.Err() != nil {
		return 0, fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	addrs, err := dc.storage.All()
	if err != nil {
		return 0, fmt.Errorf("storage all: %w", err)
	}

	// 按地址去重
	uniqueAddrs := make(map[string]struct{})
	for _, addr := range addrs {
		uniqueAddrs[addr] = struct{}{}
	}

	ctx, cancel := context.WithTimeout(ctx, dc.getTimeout())
	defer cancel()

	var count int64
	for addr := range uniqueAddrs {
		conn, err := dc.pool.get(addr)
		if err != nil {
			continue
		}

		client := websocketpb.NewWebsocketClient(conn)
		resp, err := client.Broadcast(ctx, &websocketpb.BroadcastRequest{Data: message})
		if err != nil {
			continue
		}
		count += resp.Count
	}

	return count, nil
}

// DistClientOption 配置 DistClient。
type DistClientOption func(dc *DistClient)

// WithDistTimeout 设置 gRPC 调用超时。
func WithDistTimeout(timeout time.Duration) DistClientOption {
	return func(dc *DistClient) { dc.timeout = timeout }
}

// ===== 向后兼容 =====

// DistSession 已废弃，请使用 Session。
// Deprecated: 使用 NewSession(hub, WithStorage(storage), WithAddr(addr)) 代替。
type DistSession = Session

// NewDistSession 已废弃，请使用 NewSession。
// Deprecated: 使用 NewSession(hub, WithStorage(storage), WithAddr(addr), opts...) 代替。
func NewDistSession(hub *Hub, storage Storage, addr string, opts ...Option) *Session {
	// 转换 Option 为 SessionOption
	sessionOpts := make([]SessionOption, 0, len(opts)+2)
	sessionOpts = append(sessionOpts, WithStorage(storage), WithAddr(addr))

	// 创建临时 client 来提取选项
	client := &Client{hub: hub}
	for _, opt := range opts {
		opt(client)
	}
	if client.id != "" {
		sessionOpts = append(sessionOpts, WithSessionID(client.id))
	}

	return NewSession(hub, sessionOpts...)
}

// 全局变量（向后兼容，标记为废弃）

// grpcClientPool 全局连接池。
// Deprecated: 连接池现在由 DistClient 内部管理。
var grpcClientPool sync.Map

// CloseGrpcPool 关闭全局连接池。
// Deprecated: 请使用 DistClient.Close() 代替。
func CloseGrpcPool() {
	grpcClientPool.Range(func(key, value interface{}) bool {
		grpcClientPool.Delete(key)
		if pc, ok := value.(*pooledConn); ok {
			if pc.conn != nil {
				_ = pc.conn.Close()
			}
		} else if conn, ok := value.(*grpc.ClientConn); ok {
			if conn != nil {
				_ = conn.Close()
			}
		}
		return true
	})
}
