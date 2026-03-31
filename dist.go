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
// 管理 gRPC 连接的生命周期，包括创建、缓存、过期清理和关闭。
type grpcPool struct {
	mu      sync.RWMutex
	conns   map[string]*pooledConn
	cleanup *time.Ticker
	done    chan struct{}
}

// pooledConn 表示池中的单个连接条目。
// 包含 gRPC 连接和最后使用时间戳（用于过期清理）。
type pooledConn struct {
	conn     *grpc.ClientConn
	lastUsed int64 // 最后使用时间（UnixNano 格式）
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

// cleanExpired 清理超过 TTL 的过期连接。
// 在加锁状态下遍历连接池，关闭并删除过期连接。
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

// get 从连接池获取指定地址的 gRPC 连接。
// 如果连接存在且状态健康，更新使用时间并返回；
// 如果连接不存在或状态异常，创建新连接并加入池中。
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
		// 已经关闭，直接返回避免重复关闭 channel 导致 panic
		return
	default:
		close(p.done) // 发送关闭信号给 runCleanup goroutine
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

// callRemote 是一个辅助函数，处理 Emit 和 Online 的公共逻辑：
// 检查 context、获取地址、获取连接，然后调用指定的 gRPC 方法。
func (dc *DistClient) callRemote(ctx context.Context, id string, fn func(websocketpb.WebsocketClient, context.Context) (bool, error)) (bool, error) {
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
	return fn(client, ctx)
}

// Emit 向指定客户端发送消息。
// 先检查父 context 是否已取消，避免无意义的存储查询和 gRPC 调用。
func (dc *DistClient) Emit(ctx context.Context, id string, message []byte) (bool, error) {
	return dc.callRemote(ctx, id, func(client websocketpb.WebsocketClient, ctx context.Context) (bool, error) {
		resp, err := client.Emit(ctx, &websocketpb.EmitRequest{
			Id:   id,
			Data: message,
		})
		if err != nil {
			return false, fmt.Errorf("grpc emit: %w", err)
		}
		return resp.Success, nil
	})
}

// Online 检查客户端是否在线。
// 通过存储查询客户端所在节点，然后向该节点发送在线检查请求。
// 先检查父 context 是否已取消，避免无意义的存储查询和 gRPC 调用。
func (dc *DistClient) Online(ctx context.Context, id string) (bool, error) {
	return dc.callRemote(ctx, id, func(client websocketpb.WebsocketClient, ctx context.Context) (bool, error) {
		resp, err := client.Online(ctx, &websocketpb.OnlineRequest{Id: id})
		if err != nil {
			return false, fmt.Errorf("grpc online: %w", err)
		}
		return resp.Online, nil
	})
}

// Broadcast 广播消息到所有节点。
// 首先从存储获取所有节点地址，去重后向每个节点发送广播请求。
// 先检查父 context 是否已取消，避免无意义的存储查询和 gRPC 调用。
// 返回成功发送的节点数量和可能的部分错误。
// 即使部分节点失败，也会返回已成功发送的计数。
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
	var errs []error

	for addr := range uniqueAddrs {
		conn, err := dc.pool.get(addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s: connect failed: %w", addr, err))
			continue
		}

		client := websocketpb.NewWebsocketClient(conn)
		resp, err := client.Broadcast(ctx, &websocketpb.BroadcastRequest{Data: message})
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s: broadcast failed: %w", addr, err))
			continue
		}
		count += resp.Count
	}

	// 如果有错误，返回聚合错误信息
	if len(errs) > 0 {
		return count, fmt.Errorf("broadcast completed with %d errors: %v", len(errs), errs[0])
	}

	return count, nil
}

// DistClientOption 配置 DistClient。
type DistClientOption func(dc *DistClient)

// WithDistTimeout 设置 gRPC 调用超时。
func WithDistTimeout(timeout time.Duration) DistClientOption {
	return func(dc *DistClient) { dc.timeout = timeout }
}
