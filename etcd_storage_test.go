package websocket

import (
	"os"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// checkEtcdAvailable 检查 etcd 服务是否可用
func checkEtcdAvailable(t *testing.T) []string {
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		t.Skip("ETCD_ENDPOINTS not set, skipping etcd integration test")
	}
	return []string{endpoints}
}

func setupEtcdClient(t *testing.T, endpoints []string) *clientv3.Client {
	cli, err := NewEtcdClient(endpoints)
	if err != nil {
		t.Fatalf("Failed to create etcd client: %v", err)
	}
	return cli
}

func TestEtcdStorage(t *testing.T) {
	endpoints := checkEtcdAvailable(t)
	cli := setupEtcdClient(t, endpoints)
	defer cli.Close()

	prefix := "test:etcd:" + time.Now().Format("20060102150405") + ":"
	storage := NewEtcdStorage(cli, prefix)

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
		val, err := storage.Get("nonexistent_key_12345")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if val != "" {
			t.Errorf("Get = %v, want empty string", val)
		}
	})

	t.Run("Del single key", func(t *testing.T) {
		err := storage.Set("del_test", "value")
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		err = storage.Del("del_test")
		if err != nil {
			t.Fatalf("Del failed: %v", err)
		}

		val, _ := storage.Get("del_test")
		if val != "" {
			t.Errorf("Key should be deleted")
		}
	})

	t.Run("Del multiple keys", func(t *testing.T) {
		err := storage.Set("multi1", "val1")
		if err != nil {
			t.Fatalf("Set multi1 failed: %v", err)
		}
		err = storage.Set("multi2", "val2")
		if err != nil {
			t.Fatalf("Set multi2 failed: %v", err)
		}
		err = storage.Set("multi3", "val3")
		if err != nil {
			t.Fatalf("Set multi3 failed: %v", err)
		}

		err = storage.Del("multi1", "multi2", "multi3")
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
		all, err := storage.All()
		if err != nil {
			t.Fatalf("All failed: %v", err)
		}

		err = storage.Set("all1", "addr1")
		if err != nil {
			t.Fatalf("Set all1 failed: %v", err)
		}
		err = storage.Set("all2", "addr2")
		if err != nil {
			t.Fatalf("Set all2 failed: %v", err)
		}

		all, err = storage.All()
		if all["all1"] != "addr1" {
			t.Errorf("all1 = %v, want addr1", all["all1"])
		}
		if all["all2"] != "addr2" {
			t.Errorf("all2 = %v, want addr2", all["all2"])
		}
	})

	t.Run("Clear", func(t *testing.T) {
		err := storage.Set("clear1", "192.168.1.1:8080")
		if err != nil {
			t.Fatalf("Set clear1 failed: %v", err)
		}
		err = storage.Set("clear2", "192.168.1.1:8080")
		if err != nil {
			t.Fatalf("Set clear2 failed: %v", err)
		}
		err = storage.Set("clear3", "192.168.1.2:8080")
		if err != nil {
			t.Fatalf("Set clear3 failed: %v", err)
		}

		err = storage.Clear("192.168.1.1:8080")
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		if v, _ := storage.Get("clear1"); v != "" {
			t.Error("clear1 should be deleted")
		}
		if v, _ := storage.Get("clear2"); v != "" {
			t.Error("clear2 should be deleted")
		}
		if v, _ := storage.Get("clear3"); v != "192.168.1.2:8080" {
			t.Error("clear3 should still exist")
		}
	})
}

// TestEtcdStorageConcurrent 测试并发访问
func TestEtcdStorageConcurrent(t *testing.T) {
	endpoints := checkEtcdAvailable(t)
	cli := setupEtcdClient(t, endpoints)
	defer cli.Close()

	prefix := "test:concurrent:" + time.Now().Format("20060102150405") + ":"
	storage := NewEtcdStorage(cli, prefix)

	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(n int) {
			defer func() { done <- true }()
			for j := 0; j < 10; j++ {
				key := string(rune('A'+n)) + string(rune(j))
				if err := storage.Set(key, "value"); err != nil {
					t.Errorf("Set failed: %v", err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestEtcdStorageTimeout 测试超时行为
func TestEtcdStorageTimeout(t *testing.T) {
	endpoints := checkEtcdAvailable(t)
	cli := setupEtcdClient(t, endpoints)
	defer cli.Close()

	prefix := "test:timeout:" + time.Now().Format("20060102150405") + ":"
	storage := NewEtcdStorage(cli, prefix)

	start := time.Now()
	err := storage.Set("timeout_test", "value")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if elapsed > time.Second {
		t.Errorf("Set took too long: %v", elapsed)
	}
}

// TestNewEtcdClient 测试 NewEtcdClient 函数
func TestNewEtcdClient(t *testing.T) {
	endpoints := checkEtcdAvailable(t)

	cli, err := NewEtcdClient(endpoints)
	if err != nil {
		t.Fatalf("NewEtcdClient failed: %v", err)
	}
	defer cli.Close()

	storage := NewEtcdStorage(cli, "test:client:")
	if err := storage.Set("ping", "pong"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := storage.Get("ping")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "pong" {
		t.Errorf("Get = %v, want pong", val)
	}
}

// TestEtcdStorageSetError 测试 Set 错误处理
func TestEtcdStorageSetError(t *testing.T) {
	endpoints := checkEtcdAvailable(t)
	cli := setupEtcdClient(t, endpoints)
	defer cli.Close()

	storage := NewEtcdStorage(cli, "test:error:")

	err := storage.Set("empty_value", "")
	if err != nil {
		t.Fatalf("Set empty value should succeed: %v", err)
	}

	val, err := storage.Get("empty_value")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "" {
		t.Errorf("Get = %v, want empty string", val)
	}
}

// TestEtcdStorageDelError 测试 Del 错误处理
func TestEtcdStorageDelError(t *testing.T) {
	endpoints := checkEtcdAvailable(t)
	cli := setupEtcdClient(t, endpoints)
	defer cli.Close()

	storage := NewEtcdStorage(cli, "test:del:error:")

	err := storage.Del("nonexistent_key")
	if err != nil {
		t.Fatalf("Del non-existent key should not error: %v", err)
	}
}

// TestEtcdStorageAllEmpty 测试 All 在空数据时的行为
func TestEtcdStorageAllEmpty(t *testing.T) {
	endpoints := checkEtcdAvailable(t)
	cli := setupEtcdClient(t, endpoints)
	defer cli.Close()

	prefix := "test:all:empty:" + time.Now().Format("20060102150405") + ":"
	storage := NewEtcdStorage(cli, prefix)

	all, err := storage.All()
	if err != nil {
		t.Fatalf("All failed: %v", err)
	}

	if len(all) != 0 {
		t.Errorf("All returned %d items, want 0 for empty storage", len(all))
	}
}
