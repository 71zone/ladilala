package driver

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestE2EJobKillsBrowserOnAppDeath (spike R4) force-kills the launcher
// process and asserts that no process using the profile directory survives.
func TestE2EJobKillsBrowserOnAppDeath(t *testing.T) {
	if os.Getenv("LADILALA_E2E") == "" {
		t.Skip("LADILALA_E2E not set")
	}
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "winspike.exe")
	if out, err := exec.Command("go", "build", "-o", exe, "github.com/8bu/ladilala/cmd/winspike").CombinedOutput(); err != nil {
		t.Fatalf("build winspike: %v\n%s", err, out)
	}
	dataDir := filepath.Join(tmp, "r4-profile")
	cmd := exec.Command(exe, "--engine", "chrome", "--datadir", dataDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	pidRe := regexp.MustCompile(`^PID=(\d+)`)
	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var browserPID string
	timeout := time.After(60 * time.Second)
wait:
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("winspike exited before printing PID")
			}
			if m := pidRe.FindStringSubmatch(l); m != nil {
				browserPID = m[1]
				break wait
			}
		case <-timeout:
			t.Fatal("no PID line within 60s")
		}
	}
	// Let Chrome spawn its helpers (GPU, network, renderer) first.
	time.Sleep(3 * time.Second)
	if n := len(procsUsing(t, dataDir)); n < 2 {
		t.Fatalf("expected chrome plus helpers using the profile before the kill, found %d (browser pid %s)", n, browserPID)
	}

	if out, err := exec.Command("taskkill", "/f", "/pid", strconv.Itoa(cmd.Process.Pid)).CombinedOutput(); err != nil {
		t.Fatalf("taskkill: %v\n%s", err, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	var left []string
	for time.Now().Before(deadline) {
		if left = procsUsing(t, dataDir); len(left) == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("orphan processes still use %s after launcher kill:\n%s", dataDir, strings.Join(left, "\n"))
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Join([]string{string(rune(0))}, "")) + intToString(n)
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// procsUsing returns "pid name" for every process whose command line
// contains dir.
func procsUsing(t *testing.T, dir string) []string {
	t.Helper()
	script := "Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine.Contains($env:SPIKE_DIR) } | ForEach-Object { \"$($_.ProcessId) $($_.Name)\" }"
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "SPIKE_DIR="+dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query processes: %v", err)
	}
	var res []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			res = append(res, l)
		}
	}
	return res
}
