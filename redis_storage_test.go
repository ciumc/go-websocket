package websocket

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

// setupMiniredis 创建 miniredis 服务器
func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return mr, client
}

// TestRedisStorageWithMiniredis 使用 miniredis 测试 Redis 存储
func TestRedisStorageWithMiniredis(t *testing.T) {
	mr, client := setupMiniredis(t)
	defer mr.Close()
	defer client.Close()

	storage := NewRedisStorage(client, "test:prefix")

	t.Run("Set and Get", func(t *testing.T) {
		err := storage.Set("client1", "192.168.1.1:8080")
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		val, err := storage.Get("client1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if val != "192.168.1.1:8080" {
			t.Errorf("Get = %v, want 192.168.1.1:8080", val)
		}
	})

	t.Run("Get non-existent key", func(t *testing.T) {
		val, err := storage.Get("nonexistent")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if val != "" {
			t.Errorf("Get = %v, want empty string", val)
		}
	})

	t.Run("Del", func(t *testing.T) {
		storage.Set("del_test", "value")
		err := storage.Del("del_test")
		if err != nil {
			t.Fatalf("Del failed: %v", err)
		}

		val, _ := storage.Get("del_test")
		if val != "" {
			t.Errorf("Key should be deleted")
		}
	})

	t.Run("Del multiple keys", func(t *testing.T) {
		storage.Set("multi1", "val1")
		storage.Set("multi2", "val2")
		storage.Set("multi3", "val3")

		err := storage.Del("multi1", "multi2", "multi3")
		if err != nil {
			t.Fatalf("Del multiple failed: %v", err)
		}

		for _, key := range []string{"multi1", "multi2", "multi3"} {
			val, _ := storage.Get(key)
			if val != "" {
				t.Errorf("Key %s should be deleted", key)
			}
		}
	})

	t.Run("All", func(t *testing.T) {
		// 清空之前的数据
		storage.Del("client1")

		storage.Set("all1", "addr1")
		storage.Set("all2", "addr2")

		all, err := storage.All()
		if err != nil {
			t.Fatalf("All failed: %v", err)
		}

		// 检查返回的 map 包含我们设置的数据
		if len(all) < 2 {
			t.Errorf("All returned %d items, want at least 2", len(all))
		}
	})

	t.Run("Clear", func(t *testing.T) {
		storage.Set("clear1", "192.168.1.1:8080")
		storage.Set("clear2", "192.168.1.1:8080")
		storage.Set("clear3", "192.168.1.2:8080")

		err := storage.Clear("192.168.1.1:8080")
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		// 验证 clear1 和 clear2 被删除
		if v, _ := storage.Get("clear1"); v != "" {
			t.Error("clear1 should be deleted")
		}
		if v, _ := storage.Get("clear2"); v != "" {
			t.Error("clear2 should be deleted")
		}
		// clear3 不应该被删除
		if v, _ := storage.Get("clear3"); v != "192.168.1.2:8080" {
			t.Error("clear3 should still exist")
		}
	})
}

// TestRedisStorageConcurrent 测试并发访问
func TestRedisStorageConcurrent(t *testing.T) {
	mr, client := setupMiniredis(t)
	defer mr.Close()
	defer client.Close()

	storage := NewRedisStorage(client, "concurrent:test")

	done := make(chan bool)

	// 并发写入
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 10; j++ {
				key := string(rune('A' + n))
				storage.Set(key, "value")
			}
			done <- true
		}(i)
	}

	// 等待所有写入完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestRedisStorageTimeout 测试超时行为
func TestRedisStorageTimeout(t *testing.T) {
	mr, client := setupMiniredis(t)
	defer mr.Close()
	defer client.Close()

	storage := NewRedisStorage(client, "timeout:test")

	// 正常操作应该很快
	start := time.Now()
	storage.Set("timeout_test", "value")
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("Set took too long: %v", elapsed)
	}
}

// TestRedisStorageAllEmptyHash 测试 All 方法处理空 Hash 的情况（覆盖 redis.Nil 分支）
func TestRedisStorageAllEmptyHash(t *testing.T) {
	mr, client := setupMiniredis(t)
	defer mr.Close()
	defer client.Close()

	storage := NewRedisStorage(client, "empty:hash:test")

	// 测试空 Hash - 应该返回 nil, nil（覆盖 redis.Nil 分支）
	all, err := storage.All()
	if err != nil {
		t.Fatalf("All() on empty hash failed: %v", err)
	}
	// 空 Hash 应该返回空 map 而不是 nil
	if all == nil {
		t.Log("All() returned nil for empty hash (acceptable)")
	} else if len(all) != 0 {
		t.Errorf("All() returned %d items, want 0", len(all))
	}
}

// TestRedisStorageNewRedisClientError 测试 NewRedisClient 连接错误
func TestRedisStorageNewRedisClientError(t *testing.T) {
	// 尝试连接到不存在的 Redis 服务器
	client, err := NewRedisClient(&redis.Options{
		Addr:     "localhost:9999", // 不存在的端口
		Password: "",
		DB:       0,
	})
	if err == nil {
		client.Close()
		t.Error("NewRedisClient should fail with non-existent server")
	}
	// 预期会有连接错误
	t.Logf("Expected connection error: %v", err)
}
