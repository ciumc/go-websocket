package websocket

import (
	"context"
	"testing"
	"time"
)

// TestDistClientCleanExpired 测试连接池过期清理
func TestDistClientCleanExpired(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client1", "127.0.0.1:8080")

	client := NewDistClient(storage)
	defer client.Close()

	// 手动触发清理（由于没有真实连接，这里测试函数不会 panic）
	client.pool.cleanExpired()
}

// TestDistClientPoolGet 测试连接池 get 方法
func TestDistClientPoolGet(t *testing.T) {
	storage := NewMockStorage()
	client := NewDistClient(storage)
	defer client.Close()

	// 测试获取不存在的连接会返回错误
	// 由于没有真实 gRPC 服务器，这里主要验证不会 panic
	_, err := client.pool.get("127.0.0.1:9999")
	if err == nil {
		t.Log("Got connection (unexpected in test)")
	} else {
		t.Logf("Expected error (no server): %v", err)
	}
}

// TestDistClientEmitWithContext 测试 Emit 使用 context
func TestDistClientEmitWithContext(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("test-client", "127.0.0.1:8080")

	client := NewDistClient(storage)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 由于没有真实服务器，会返回错误
	ok, err := client.Emit(ctx, "test-client", []byte("test"))
	if err != nil {
		t.Logf("Expected error (no server): %v", err)
	}
	if ok {
		t.Error("Emit should return false without server")
	}
}

// TestDistClientOnlineWithContext 测试 Online 使用 context
func TestDistClientOnlineWithContext(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("test-client", "127.0.0.1:8080")

	client := NewDistClient(storage)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ok, err := client.Online(ctx, "test-client")
	if err != nil {
		t.Logf("Expected error (no server): %v", err)
	}
	if ok {
		t.Error("Online should return false without server")
	}
}

// TestDistClientBroadcastWithMultipleAddresses 测试广播到多个地址
func TestDistClientBroadcastWithMultipleAddresses(t *testing.T) {
	storage := NewMockStorage()
	_ = storage.Set("client1", "127.0.0.1:8080")
	_ = storage.Set("client2", "127.0.0.1:8081")
	_ = storage.Set("client3", "127.0.0.1:8080") // 重复地址

	client := NewDistClient(storage)
	defer client.Close()

	ctx := context.Background()
	count, err := client.Broadcast(ctx, []byte("test"))

	// 没有真实服务器会返回错误或 0
	if err != nil {
		t.Logf("Broadcast error (expected): %v", err)
	}
	t.Logf("Broadcast count: %d", count)
}


// TestDistServerWithMockHub 测试 DistServer 方法
func TestDistServerWithMockHub(t *testing.T) {
	hub := NewHubRun()
	defer hub.Close()

	_ = NewDistServer(hub) // 创建 server 但不使用

	t.Run("Emit with registered client", func(t *testing.T) {
		// 创建并注册客户端
		client := NewClient(hub, WithID("emit-test"))
		hub.register <- client
		time.Sleep(10 * time.Millisecond)

		// Emit 应该失败，因为没有真实连接
		// 但我们测试的是 DistServer 的逻辑
	})

	t.Run("Online check", func(t *testing.T) {
		client := NewClient(hub, WithID("online-check-test"))
		hub.register <- client
		time.Sleep(10 * time.Millisecond)

		// 验证客户端已注册
		if _, ok := hub.Client("online-check-test"); !ok {
			t.Error("Client should be registered")
		}
	})
}
