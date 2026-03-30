package websocket

import (
	"net"
	"sync"
	"testing"
)

// TestIP 测试 IP() 函数返回值非 nil
func TestIP(t *testing.T) {
	ip := IP()
	if ip == nil {
		t.Error("IP() 返回 nil，期望返回有效的 IP 地址")
	}
}

// TestIPSingleton 测试 sync.Once 单例行为，多次调用返回相同结果
func TestIPSingleton(t *testing.T) {
	// 重置 sync.Once 和 _ip 以测试单例行为
	// 注意：这里通过多次调用验证返回相同结果
	ip1 := IP()
	ip2 := IP()
	ip3 := IP()

	if ip1 == nil {
		t.Fatal("第一次调用 IP() 返回 nil")
	}

	if !ip1.Equal(ip2) {
		t.Errorf("单例测试失败：第一次调用返回 %v，第二次调用返回 %v", ip1, ip2)
	}

	if !ip1.Equal(ip3) {
		t.Errorf("单例测试失败：第一次调用返回 %v，第三次调用返回 %v", ip1, ip3)
	}
}

// TestIPConcurrent 多 goroutine 并发调用 IP()，验证无竞态条件且结果一致
func TestIPConcurrent(t *testing.T) {
	const numGoroutines = 100
	var wg sync.WaitGroup
	results := make(chan net.IP, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- IP()
		}()
	}

	wg.Wait()
	close(results)

	var firstIP net.IP
	count := 0
	for ip := range results {
		if count == 0 {
			firstIP = ip
			if firstIP == nil {
				t.Fatal("并发调用 IP() 返回 nil")
			}
		} else {
			if !firstIP.Equal(ip) {
				t.Errorf("并发测试结果不一致：期望 %v，实际 %v", firstIP, ip)
			}
		}
		count++
	}

	if count != numGoroutines {
		t.Errorf("期望 %d 个结果，实际得到 %d 个", numGoroutines, count)
	}
}

// TestIPFormat 验证返回的 IP 格式有效（IPv4 或 IPv6）
func TestIPFormat(t *testing.T) {
	ip := IP()
	if ip == nil {
		t.Fatal("IP() 返回 nil")
	}

	// 验证是有效的 IPv4 地址
	if ip.To4() == nil {
		t.Errorf("IP() 返回的地址不是有效的 IPv4 格式: %v", ip)
	}

	// 验证不是回环地址
	if ip.IsLoopback() {
		t.Errorf("IP() 返回了回环地址: %v", ip)
	}

	// 验证字符串表示不为空
	ipStr := ip.String()
	if ipStr == "" {
		t.Error("IP().String() 返回空字符串")
	}

	// 验证可以通过 net.ParseIP 解析
	parsedIP := net.ParseIP(ipStr)
	if parsedIP == nil {
		t.Errorf("无法解析 IP 字符串: %s", ipStr)
	}
}
