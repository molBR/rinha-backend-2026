package main

import (
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"rinha-backend-2026/internal/fraud"
	"rinha-backend-2026/internal/rawhttp"
)

var engine *fraud.Engine

// apiHandler wires fraud.Engine into the rawhttp.Handler interface.
type apiHandler struct{}

func (h *apiHandler) ServeFraudScore(body []byte) []byte {
	idx, _ := engine.ParseAndScore(body)
	return rawhttp.FraudResponse(idx)
}

func (h *apiHandler) ServeReady() []byte {
	return rawhttp.ReadyResponse()
}

func main() {
	if v := os.Getenv("GOMAXPROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
		}
	}

	refsPath := getenv("REFERENCES_PATH", "resources/references.json.gz")
	mccPath := getenv("MCC_RISK_PATH", "resources/mcc_risk.json")
	normPath := getenv("NORMALIZATION_PATH", "resources/normalization.json")

	var err error
	engine, err = fraud.NewEngine(refsPath, mccPath, normPath)
	if err != nil {
		log.Fatalf("init engine: %v", err)
	}

	srv := rawhttp.New(&apiHandler{})

	// Unix socket: receives TCP file descriptors from the SCM_RIGHTS LB.
	// The LB passes the accepted client connection FD directly to us,
	// eliminating all HTTP proxying overhead.
	ctrlSocket := os.Getenv("CTRL_SOCKET")
	if ctrlSocket != "" {
		_ = os.Remove(ctrlSocket)
		ctrlLn, err := net.Listen("unix", ctrlSocket)
		if err != nil {
			log.Fatalf("ctrl socket listen %s: %v", ctrlSocket, err)
		}
		if err := os.Chmod(ctrlSocket, 0666); err != nil {
			log.Printf("chmod ctrl socket: %v", err)
		}
		log.Printf("SCM_RIGHTS control socket ready: %s", ctrlSocket)
		go serveSCMRights(ctrlLn, srv)
	}

	// TCP listener: serves /ready for healthcheck AND as fallback if no LB.
	port := getenv("PORT", "8080")
	tcpLn, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("listen tcp :%s: %v", port, err)
	}
	log.Printf("listening on :%s (rawhttp)", port)

	for {
		conn, err := tcpLn.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go srv.ServeConn(conn) //nolint:errcheck
	}
}

// serveSCMRights receives TCP file descriptors from the LB via Unix socket
// and handles each connection with the rawhttp server.
func serveSCMRights(ln net.Listener, srv *rawhttp.Server) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go receiveAndServe(conn.(*net.UnixConn), srv)
	}
}

// receiveAndServe reads FDs from a Unix control connection and dispatches
// each received TCP connection to the rawhttp server.
func receiveAndServe(uc *net.UnixConn, srv *rawhttp.Server) {
	defer uc.Close()
	buf := make([]byte, 1)
	oob := make([]byte, 24) // enough for one SCM_RIGHTS message (16B cmsghdr + 4B fd + 4B pad)

	for {
		_, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
		if err != nil {
			return
		}

		fd := parseUnixRights(oob[:oobn])
		if fd < 0 {
			continue
		}

		file := os.NewFile(uintptr(fd), "tcp-conn")
		tcpConn, err := net.FileConn(file)
		_ = file.Close() // net.FileConn dup'd the FD; close our copy
		if err != nil {
			_ = syscall.Close(fd)
			continue
		}

		if tc, ok := tcpConn.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
			_ = tc.SetKeepAlive(true)
		}

		go srv.ServeConn(tcpConn) //nolint:errcheck
	}
}

// parseUnixRights extracts the first file descriptor from an SCM_RIGHTS
// control message. Returns -1 if none found.
func parseUnixRights(oob []byte) int {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil || len(msgs) == 0 {
		return -1
	}
	fds, err := syscall.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		return -1
	}
	return fds[0]
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
