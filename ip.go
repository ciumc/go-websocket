package websocket

import (
	"net"
	"sync"
)

var (
	// _ip 存储已确定的本地 IP 地址
	_ip net.IP

	// _ipOnce 确保 IP 地址只被确定一次
	_ipOnce sync.Once
)

// IP 返回本机的本地 IP 地址。
// 它会查找系统上第一个可用的非回环 IPv4 地址。
// 结果被缓存，因此后续调用返回相同的值。
// @Description: 获取本地 IP 地址
// @return net.IP 本地 IP 地址，如果无法确定则返回 nil
func IP() net.IP {
	_ipOnce.Do(func() {
		as, _ := net.InterfaceAddrs()
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
	})
	return _ip
}
