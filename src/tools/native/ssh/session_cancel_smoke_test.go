package ssh

import (
	"AgenticService/src/domain"
	"AgenticService/src/tools"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func TestSSHCancelWhileOpeningSessionSmoke(t *testing.T) {
	// 完全在記憶體產生測試身分，不讀取使用者設定、SSH key 或 known_hosts。
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &gossh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	opened := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		server, channels, requests, err := gossh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go gossh.DiscardRequests(requests)
		if _, ok := <-channels; !ok {
			return
		}
		close(opened)
		<-release // 刻意不回應 channel-open。
	}()
	tool := New(map[string]Profile{"synthetic": {Address: listener.Addr().String(), User: "synthetic", Password: "synthetic", HostKeySHA256: gossh.FingerprintSHA256(signer.PublicKey()), ConnectAttempts: 1}}, 1024, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		execution domain.ToolExecution
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := tool.Execute(ctx, tools.Invocation{Call: domain.ToolCall{Name: "ssh_exec", Arguments: map[string]any{"profile": "synthetic", "command": "never-dispatched"}}}, nil)
		done <- outcome{result, err}
	}()
	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("test server did not receive session request")
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || domain.ToolExecutionState(result.execution.Details) != domain.ToolNotDispatched {
			t.Fatalf("cancel result=%+v err=%v", result.execution, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("session-open ignored cancellation")
	}
}
