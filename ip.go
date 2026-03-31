package websocket

import (
	"log"
	"net"
	"sync"
)

var (
	// _ip 存储已确定的本地 IP 地址
	_ip net.IP

	// _ipOnce 确保 IP 地址只被确定一次
	// 并发安全: sync.Once 保证并发调用 IP() 时只执行一次初始化
	_ipOnce sync.Once

	// _ipErr 存储 IP 获取过程中的错误
	_ipErr error
)

// IP 返回本机的本地 IP 地址。
// 它会查找系统上第一个可用的非回环 IPv4 地址。
// 结果被缓存，因此后续调用返回相同的值。
//
// 并发安全:
//   - 使用 sync.Once 确保初始化只执行一次
//   - 多个 goroutine 可安全并发调用
//   - 返回值是 net.IP 类型（不可变）
//
// 如果无法确定 IP 地址（例如没有网络接口或所有接口都是回环），
// 返回 nil。调用者应该检查返回值是否为 nil。
//
// 使用示例:
//
//	ip := websocket.IP()
//	if ip == nil {
//	    log.Fatal("无法获取本地 IP")
//	}
//	addr := ip.String() + ":8080"
func IP() net.IP {
	_ipOnce.Do(func() {
		as, err := net.InterfaceAddrs()
		if err != nil {
			_ipErr = err
			log.Printf("websocket: failed to get interface addresses: %v", err)
			return
		}
		for _, a := range as {
			inet, ok := a.(*net.IPNet)
			if !ok || inet.IP.IsLoopback() {
				continue
			}
			ip := inet.IP.To4()
			if ip == nil {
				continue
			}
			_ip = ip
			return
		}
		log.Printf("websocket: no non-loopback IPv4 address found")
	})
	return _ip
}
