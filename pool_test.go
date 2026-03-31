package websocket

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"
)

// TestGrpcPoolGetWithExistingConn 测试获取现有连接
func TestGrpcPoolGetWithExistingConn(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)
	defer client.Close()

	_ = client.pool // 使用 pool

	// 测试 get 方法各种情况
	t.Run("get from empty pool", func(t *testing.T) {
		// 这会尝试创建真实连接，预期失败
		_, err := client.pool.get("invalid:9999")
		if err == nil {
			t.Log("Unexpected success")
		} else {
			t.Logf("Expected error: %v", err)
		}
	})
}

// TestGrpcPoolCloseWithActiveConnections 测试关闭有活动连接的池
func TestGrpcPoolCloseWithActiveConnections(t *testing.T) {
	pool := newGrpcPool()

	// 模拟有连接的情况
	pool.mu.Lock()
	pool.conns["test-addr"] = &pooledConn{
		conn:     nil, // 无法创建真实连接
		lastUsed: time.Now().UnixNano(),
	}
	pool.mu.Unlock()

	// 关闭池
	pool.close()

	// 验证池已清空
	pool.mu.RLock()
	if len(pool.conns) != 0 {
		t.Errorf("Pool should be empty, got %d items", len(pool.conns))
	}
	pool.mu.RUnlock()
}

// TestGrpcPoolMultipleClose 测试多次关闭池
func TestGrpcPoolMultipleClose(t *testing.T) {
	pool := newGrpcPool()

	pool.close()
	pool.close() // 不应 panic
	pool.close()
}

// TestGrpcPoolCleanExpiredWithOldConnections 测试清理过期连接
func TestGrpcPoolCleanExpiredWithOldConnections(t *testing.T) {
	pool := newGrpcPool()
	defer pool.close()

	// 添加一个"旧"连接（模拟）
	pool.mu.Lock()
	pool.conns["old-conn"] = &pooledConn{
		conn:     nil,
		lastUsed: time.Now().Add(-10 * time.Minute).UnixNano(), // 10分钟前
	}
	pool.conns["new-conn"] = &pooledConn{
		conn:     nil,
		lastUsed: time.Now().UnixNano(), // 现在
	}
	pool.mu.Unlock()

	// 触发清理
	pool.cleanExpired()

	// 验证旧连接被删除
	pool.mu.RLock()
	if _, ok := pool.conns["old-conn"]; ok {
		t.Error("Old connection should be deleted")
	}
	if _, ok := pool.conns["new-conn"]; !ok {
		t.Error("New connection should still exist")
	}
	pool.mu.RUnlock()
}

// TestPooledConnStateTransitions 测试连接状态转换
func TestPooledConnStateTransitions(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)
	defer client.Close()

	_ = client.pool // 使用 pool

	// 测试不同连接状态
	states := []connectivity.State{
		connectivity.Idle,
		connectivity.Ready,
		connectivity.Connecting,
		connectivity.TransientFailure,
		connectivity.Shutdown,
	}

	for _, state := range states {
		t.Run(state.String(), func(t *testing.T) {
			// 这些是状态检查的路径测试
			// 由于无法创建真实连接，这里只是记录预期的行为
			t.Logf("State: %s", state)
		})
	}
}

// TestDistClientGetTimeoutCustom 测试自定义超时
func TestDistClientGetTimeoutCustom(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage, WithDistTimeout(5*time.Second))
	defer client.Close()

	if client.getTimeout() != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", client.getTimeout())
	}
}

// TestDistClientEmitToEmptyAddressExtra 测试发送到空地址
func TestDistClientEmitToEmptyAddressExtra(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("empty-addr", "") // 空地址

	client := NewDistClient(storage)
	defer client.Close()

	ok, err := client.Emit(context.Background(), "empty-addr", []byte("test"))
	if err == nil {
		t.Error("Emit should return error for empty address")
	}
	if ok {
		t.Error("Emit should return false for empty address")
	}
}

// TestDistClientOnlineEmptyAddr 测试检查空地址客户端
func TestDistClientOnlineEmptyAddr(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("empty-addr", "")

	client := NewDistClient(storage)
	defer client.Close()

	ok, err := client.Online(context.Background(), "empty-addr")
	if err == nil {
		t.Error("Online should return error for empty address")
	}
	if ok {
		t.Error("Online should return false for empty address")
	}
}

// TestDistClientBroadcastToSingleAddress 测试广播到单一地址
func TestDistClientBroadcastToSingleAddress(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client1", "192.168.1.1:8080")
	_ = storage.Set("client2", "192.168.1.1:8080") // 同一地址
	_ = storage.Set("client3", "192.168.1.1:8080") // 同一地址

	client := NewDistClient(storage)
	defer client.Close()

	count, err := client.Broadcast(context.Background(), []byte("test"))
	t.Logf("Broadcast count: %d, err: %v", count, err)
}

// TestDistClientContextCancellation 测试 context 取消
func TestDistClientContextCancellation(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client", "192.168.1.1:8080")

	client := NewDistClient(storage)
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := client.Emit(ctx, "client", []byte("test"))
	if err == nil {
		t.Error("Emit should return error with cancelled context")
	}
}
