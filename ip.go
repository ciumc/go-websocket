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
	_ipOnce sync.Once

	// _ipErr 存储 IP 获取过程中的错误
	_ipErr error
)

// IP 返回本机的本地 IP 地址。
// 它会查找系统上第一个可用的非回环 IPv4 地址。
// 结果被缓存，因此后续调用返回相同的值。
//
// 如果无法确定 IP 地址（例如没有网络接口或所有接口都是回环），
// 返回 nil。调用者应该检查返回值是否为 nil。
//
// @Description: 获取本地 IP 地址
// @return net.IP 本地 IP 地址，如果无法确定则返回 nil
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
