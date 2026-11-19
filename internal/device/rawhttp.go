package device

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// rawDo sends one HTTP/1.0 request and reads until the clock closes the connection.
//
// GeekMagic firmwares frame some responses badly (duplicate Content-Length, bytes after
// "Connection: close"), which net/http rejects even though the request succeeded. Reading the
// raw stream and parsing only the status line is what every working client for these devices does.
func rawDo(ctx context.Context, base *url.URL, method, path, contentType string, body []byte) (int, []byte, error) {
	host := base.Host
	if base.Port() == "" {
		host = net.JoinHostPort(base.Hostname(), "80")
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)

	var req bytes.Buffer
	fmt.Fprintf(&req, "%s %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\nUser-Agent: claude-tv\r\n", method, path, base.Host)
	if body != nil {
		fmt.Fprintf(&req, "Content-Type: %s\r\nContent-Length: %d\r\n", contentType, len(body))
	}
	req.WriteString("\r\n")
	req.Write(body)
	if _, err := conn.Write(req.Bytes()); err != nil {
		return 0, nil, err
	}

	resp, err := io.ReadAll(io.LimitReader(conn, 1<<20))
	if len(resp) == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	// A reset after the response arrived is normal for these devices; keep what we got.
	status, payload, perr := parseRaw(resp)
	if perr != nil {
		return 0, nil, perr
	}
	return status, payload, nil
}

func parseRaw(resp []byte) (int, []byte, error) {
	line, _, _ := bufio.NewReader(bytes.NewReader(resp)).ReadLine()
	parts := strings.Fields(string(line))
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, nil, fmt.Errorf("not an HTTP response: %q", truncate(string(line), 60))
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, nil, fmt.Errorf("bad status line: %q", truncate(string(line), 60))
	}
	if i := bytes.Index(resp, []byte("\r\n\r\n")); i >= 0 {
		return status, resp[i+4:], nil
	}
	if i := bytes.Index(resp, []byte("\n\n")); i >= 0 {
		return status, resp[i+2:], nil
	}
	return status, nil, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
