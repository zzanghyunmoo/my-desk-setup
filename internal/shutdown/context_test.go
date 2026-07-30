//go:build !windows

package shutdown

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNotifyCancelsProcessContextOnInterrupt(t *testing.T) {
	if os.Getenv("MDS_SHUTDOWN_HELPER") == "1" {
		ctx, stop := Notify()
		_, _ = os.Stdout.WriteString("ready\n")
		<-ctx.Done()
		stop()
		_, _ = os.Stdout.WriteString("canceled\n")
		os.Exit(0)
	}
	command := exec.Command(os.Args[0], "-test.run=TestNotifyCancelsProcessContextOnInterrupt")
	command.Env = append(os.Environ(), "MDS_SHUTDOWN_HELPER=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe(): %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	reader := bufio.NewReader(stdout)
	ready, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(ready) != "ready" {
		_ = command.Process.Kill()
		t.Fatalf("helper readiness = %q error=%v", ready, err)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		_ = command.Process.Kill()
		t.Fatalf("Signal(): %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	canceled, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(canceled) != "canceled" {
		_ = command.Process.Kill()
		t.Fatalf("helper cancellation = %q error=%v", canceled, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait(): %v", err)
		}
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("signal-derived context did not terminate helper")
	}
}
