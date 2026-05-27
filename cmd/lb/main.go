// SCM_RIGHTS load balancer: accepts TCP connections on :9999 and passes the
// socket file descriptor directly to one of the API workers via Unix domain
// socket. This eliminates all HTTP proxy overhead — the LB uses ~5µs per
// connection (one accept + one sendmsg) vs ~1ms for HTTP proxying.
//
// The LB goroutine terminates immediately after passing the FD; no per-request
// state is held. Memory usage stays near-zero regardless of connection count.
package main

import (
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
)

func main() {
	workerEnv := os.Getenv("WORKER_SOCKETS")
	if workerEnv == "" {
		workerEnv = "/sockets/lb-ctrl-1.sock,/sockets/lb-ctrl-2.sock"
	}
	paths := strings.Split(workerEnv, ",")

	// Connect to each worker's Unix domain socket.
	workers := make([]*net.UnixConn, len(paths))
	for i, p := range paths {
		p = strings.TrimSpace(p)
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: p, Net: "unix"})
		if err != nil {
			log.Fatalf("dial worker %s: %v", p, err)
		}
		workers[i] = conn
		log.Printf("connected to worker %d: %s", i, p)
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":9999"
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", listenAddr, err)
	}

	setDeferAccept(ln)
	log.Printf("SCM_RIGHTS LB listening on %s, %d workers", listenAddr, len(workers))

	var counter atomic.Uint64
	for {
		tcpConn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}

		wi := counter.Add(1) % uint64(len(workers))

		raw, err := tcpConn.(*net.TCPConn).SyscallConn()
		if err != nil {
			tcpConn.Close()
			continue
		}

		var fd int
		if err := raw.Control(func(f uintptr) { fd = int(f) }); err != nil {
			tcpConn.Close()
			continue
		}

		// Send the accepted FD to the chosen worker via SCM_RIGHTS.
		// The kernel duplicates the FD into the receiving process's fd table.
		rights := syscall.UnixRights(fd)
		if _, _, err = workers[wi].WriteMsgUnix(nil, rights, nil); err != nil {
			log.Printf("send FD to worker %d: %v", wi, err)
		}

		// Close our copy — the worker's copy keeps the socket alive.
		tcpConn.Close()
	}
}
