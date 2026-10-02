//go:build !windows

package cluster

import (
	"net"
	"syscall"
)

// 允許同一台 Mac 的兩個獨立 Server 走與實機相同的 multicast 握手。
func configureMulticast(conn *net.UDPConn, ip net.IP) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var optionErr error
	err = raw.Control(func(fd uintptr) {
		optionErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
		if optionErr == nil {
			optionErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 1)
		}
		if optionErr == nil {
			optionErr = syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, [4]byte(ip.To4()))
		}
	})
	if err != nil {
		return err
	}
	return optionErr
}
