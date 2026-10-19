package proxy_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"air-traffic-control/backend/cmd/proxy"
)

type memoryConn struct {
	net.Conn
	r *io.PipeReader
	w *io.PipeWriter
}

func (m *memoryConn) Read(b []byte) (int, error)  { return m.r.Read(b) }
func (m *memoryConn) Write(b []byte) (int, error) { return m.w.Write(b) }
func (m *memoryConn) Close() error {
	_ = m.r.Close()
	return m.w.Close()
}
func (m *memoryConn) LocalAddr() net.Addr                { return &net.IPAddr{} }
func (m *memoryConn) RemoteAddr() net.Addr               { return &net.IPAddr{} }
func (m *memoryConn) SetDeadline(t time.Time) error      { return nil }
func (m *memoryConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *memoryConn) SetWriteDeadline(t time.Time) error { return nil }

type stubDialer struct {
	dialFn func(ctx context.Context, network, address string) (net.Conn, error)
}

func (s *stubDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if s.dialFn != nil {
		return s.dialFn(ctx, network, address)
	}
	return nil, fmt.Errorf("dial not configured")
}

func TestProxyStreams_BidirectionalPiping(t *testing.T) {
	// Setup client -> conn and conn -> client pipes
	// Client side:
	// stdin writes to inPipeWriter -> read by ForwardStreams as inPipeReader
	inPipeReader, inPipeWriter := io.Pipe()
	// stdout written by ForwardStreams as outPipeWriter -> read by test as outPipeReader
	outPipeReader, outPipeWriter := io.Pipe()

	// Remote server side:
	// conn writes to remoteRecvWriter -> read by test as remoteRecvReader
	remoteRecvReader, remoteRecvWriter := io.Pipe()
	// test writes to remoteSendWriter -> read by conn as remoteSendReader
	remoteSendReader, remoteSendWriter := io.Pipe()

	serverConn := &memoryConn{
		r: remoteSendReader,
		w: remoteRecvWriter,
	}

	dialer := &stubDialer{
		dialFn: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "100.105.71.49:22" {
				return nil, fmt.Errorf("unexpected target %s", address)
			}
			return serverConn, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := proxy.ForwardStreams(ctx, dialer, "100.105.71.49", "22", inPipeReader, outPipeWriter)
		if err != nil && err != io.EOF {
			t.Errorf("ForwardStreams failed: %v", err)
		}
	}()

	// 1. Write from stdin to remote
	go func() {
		_, _ = inPipeWriter.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
	}()

	buf := make([]byte, 64)
	n, err := remoteRecvReader.Read(buf)
	if err != nil {
		t.Fatalf("failed to read on remote: %v", err)
	}
	if string(buf[:n]) != "SSH-2.0-OpenSSH_9.6\r\n" {
		t.Fatalf("unexpected message on remote: %q", string(buf[:n]))
	}

	// 2. Write from remote to stdout
	go func() {
		_, _ = remoteSendWriter.Write([]byte("SSH-2.0-Tailscale\r\n"))
	}()

	n, err = outPipeReader.Read(buf)
	if err != nil {
		t.Fatalf("failed to read on stdout: %v", err)
	}
	if string(buf[:n]) != "SSH-2.0-Tailscale\r\n" {
		t.Fatalf("unexpected message on stdout: %q", string(buf[:n]))
	}

	// Teardown
	inPipeWriter.Close()
	remoteSendWriter.Close()
	wg.Wait()
}
