//go:build !linux

package main

import "net"

// setDeferAccept is a no-op on non-Linux platforms.
func setDeferAccept(ln net.Listener) {}
