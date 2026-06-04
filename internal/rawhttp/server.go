// Package rawhttp provides a minimal, zero-allocation HTTP/1.1 server optimized
// for the fraud detection API hot path. It parses requests manually from a
// pooled buffer and routes to precomputed JSON responses.
package rawhttp

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Handler defines the interface for request routing.
type Handler interface {
	ServeFraudScore(body []byte) []byte
	ServeReady() []byte
}

var (
	// k=5 precomputed responses: fraud_score = fraudCount/5 (exact).
	// count=0 → 0.0  (approved)
	// count=1 → 0.2  (approved, threshold <0.4)
	// count=2 → 0.4  (rejected) ← more aggressive: FN penalised 3× vs FP
	// count=3 → 0.6  (rejected)
	// count=4 → 0.8  (rejected)
	// count=5 → 1.0  (rejected)
	fraudResponses    [6][]byte
	badRequestResponse []byte
	readyResponse      []byte
	notFoundResponse   []byte
)

func init() {
	bodies := [6]string{
		`{"approved":true,"fraud_score":0.0}`,
		`{"approved":true,"fraud_score":0.2}`,
		`{"approved":false,"fraud_score":0.4}`,
		`{"approved":false,"fraud_score":0.6}`,
		`{"approved":false,"fraud_score":0.8}`,
		`{"approved":false,"fraud_score":1.0}`,
	}
	for i, body := range bodies {
		fraudResponses[i] = buildOKResponse("application/json", body)
	}
	badRequestResponse = buildResponse(400, "Bad Request", "text/plain", "bad request")
	readyResponse = buildOKResponse("", "OK")
	notFoundResponse = buildResponse(404, "Not Found", "", "")
}

func buildOKResponse(ct, body string) []byte {
	hdr := "HTTP/1.1 200 OK\r\nConnection: keep-alive\r\nKeep-Alive: timeout=60\r\n"
	if ct != "" {
		hdr += "Content-Type: " + ct + "\r\n"
	}
	hdr += "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	out := make([]byte, len(hdr)+len(body))
	copy(out, hdr)
	copy(out[len(hdr):], body)
	return out
}

func buildResponse(code int, text, ct, body string) []byte {
	hdr := "HTTP/1.1 " + strconv.Itoa(code) + " " + text + "\r\n"
	if ct != "" {
		hdr += "Content-Type: " + ct + "\r\n"
	}
	hdr += "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	out := make([]byte, len(hdr)+len(body))
	copy(out, hdr)
	copy(out[len(hdr):], body)
	return out
}

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, readBufSize)
		return &b
	},
}

var headerEndMarker = []byte("\r\n\r\n")

const (
	readBufSize    = 4096
	maxRequestSize = 8 * 1024
)

// Server is a minimal HTTP/1.1 server.
type Server struct {
	handler Handler
}

// New creates a new Server.
func New(handler Handler) *Server {
	return &Server{handler: handler}
}

// ServeConn processes an HTTP connection.
func (s *Server) ServeConn(conn net.Conn) error {
	var reqCount int
	defer func() {
		_ = conn.Close()
		if reqCount > 1 {
			pipelinedConns.Add(1)
		}
	}()

	bp := bufPool.Get().(*[]byte)
	buf := *bp
	pos := 0
	used := 0
	defer bufPool.Put(bp)

	totalConns.Add(1)
	for {
		headEnd := indexHeaderEnd(buf[pos:used])
		if headEnd < 0 {
			if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return err
			}
		}
		for headEnd < 0 {
			if n, err := conn.Read(buf[used:]); n > 0 {
				used += n
				if used-pos > maxRequestSize {
					return errRequestTooLarge
				}
				headEnd = indexHeaderEnd(buf[pos:used])
				continue
			} else if err != nil {
				if err == io.EOF && headEnd >= 0 {
					break
				}
				return err
			}
		}

		headEnd += pos + 4

		method, path, contentLen := parseRequestLine(buf[pos:headEnd])

		bodyEnd := headEnd + contentLen
		for used < bodyEnd {
			if n, err := conn.Read(buf[used:]); n > 0 {
				used += n
				continue
			} else if err != nil {
				return err
			}
		}

		var resp []byte

		switch {
		case len(path) == 12 && path[1] == 'f' && len(method) == 4 && method[0] == 'P':
			resp = s.handler.ServeFraudScore(buf[headEnd:bodyEnd])
		case len(path) == 6 && path[1] == 'r' && len(method) == 3 && method[0] == 'G':
			resp = s.handler.ServeReady()
		default:
			resp = notFoundResponse
		}

		if _, err := conn.Write(resp); err != nil {
			return err
		}

		reqCount++
		pos = bodyEnd
		if pos >= used {
			pos = 0
			used = 0
		}
	}
}

var pipelinedConns atomic.Int64
var totalConns atomic.Int64

func indexHeaderEnd(b []byte) int {
	return bytes.Index(b, headerEndMarker)
}

func parseRequestLine(buf []byte) (method, path []byte, contentLen int) {
	i := 0
	for i < len(buf) && buf[i] != ' ' {
		i++
	}
	method = buf[:i]
	i++
	pathStart := i
	for i < len(buf) && buf[i] != ' ' {
		i++
	}
	path = buf[pathStart:i]
	contentLen = findContentLength(buf)
	return
}

func findContentLength(buf []byte) int {
	for i := 0; i+16 < len(buf); i++ {
		if (buf[i] == 'C' || buf[i] == 'c') && isContentLength(buf[i:]) {
			j := i + 16
			for j < len(buf) && buf[j] == ' ' {
				j++
			}
			n := 0
			for j < len(buf) && buf[j] >= '0' && buf[j] <= '9' {
				n = n*10 + int(buf[j]-'0')
				j++
			}
			return n
		}
	}
	return 0
}

func isContentLength(b []byte) bool {
	if len(b) < 16 {
		return false
	}
	c0 := b[0]
	if c0 != 'C' && c0 != 'c' {
		return false
	}
	if b[1] != 'o' && b[1] != 'O' {
		return false
	}
	if b[2] != 'n' && b[2] != 'N' {
		return false
	}
	if b[3] != 't' && b[3] != 'T' {
		return false
	}
	if b[4] != 'e' && b[4] != 'E' {
		return false
	}
	if b[5] != 'n' && b[5] != 'N' {
		return false
	}
	if b[6] != 't' && b[6] != 'T' {
		return false
	}
	if b[7] != '-' {
		return false
	}
	if b[8] != 'l' && b[8] != 'L' {
		return false
	}
	if b[9] != 'e' && b[9] != 'E' {
		return false
	}
	if b[10] != 'n' && b[10] != 'N' {
		return false
	}
	if b[11] != 'g' && b[11] != 'G' {
		return false
	}
	if b[12] != 't' && b[12] != 'T' {
		return false
	}
	if b[13] != 'h' && b[13] != 'H' {
		return false
	}
	if b[14] != ':' {
		return false
	}
	return true
}

type requestError struct{ msg string }

func (e *requestError) Error() string { return e.msg }

var errRequestTooLarge = &requestError{"request too large"}

// FraudResponse returns the precomputed HTTP response for the given fraud count (0–5).
// With k=5, count is always in [0,5].
func FraudResponse(count int) []byte {
	if count < 0 || count >= len(fraudResponses) {
		return fraudResponses[0]
	}
	return fraudResponses[count]
}

// BadRequestResponse returns the precomputed 400 response for malformed requests.
func BadRequestResponse() []byte { return badRequestResponse }

// ReadyResponse returns the precomputed HTTP 200 response for /ready.
func ReadyResponse() []byte { return readyResponse }

// NotFoundResponse returns the precomputed 404 response.
func NotFoundResponse() []byte { return notFoundResponse }
