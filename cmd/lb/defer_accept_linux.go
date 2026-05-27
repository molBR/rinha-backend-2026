package main

import (
	"net"
	"syscall"
)

// setDeferAccept enables TCP_DEFER_ACCEPT on the listener.
// This tells the kernel not to notify the process about a new connection
// until at least some data has arrived, reducing accept() overhead.
func setDeferAccept(ln net.Listener) {
	tcpLn, ok := ln.(*net.TCPListener)
	if !ok {
		return
	}
	raw, err := tcpLn.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_DEFER_ACCEPT, 3)
	})
}
