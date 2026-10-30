package ssh

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
)

func TestIsConnectionDeadRecognisesTransportAborts(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// gVisor's tcpip.ErrAborted, surfaced through net.OpError as
		// "read tcp 100.100.90.54:41643: operation aborted".
		{"gvisor abort", fmt.Errorf("read tcp 100.100.90.54:41643: %w", errors.New("operation aborted")), true},
		{"tailnet refused", errors.New("dial tcp 100.120.7.68:22: connection was refused"), true},
		{"connection refused", errors.New("connect: connection refused"), true},
		{"reset by peer", errors.New("read tcp: connection reset by peer"), true},
		{"broken pipe", errors.New("write tcp: broken pipe"), true},
		{"closed network connection", errors.New("use of closed network connection"), true},
		{"plain eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"errno reset", syscall.ECONNRESET, true},
		{"ssh auth failure", errors.New("ssh: handshake failed: unable to authenticate"), false},
		{"remote command failure", errors.New("ssh command execution failed: exit status 1"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConnectionDead(tc.err); got != tc.want {
				t.Fatalf("isConnectionDead(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
