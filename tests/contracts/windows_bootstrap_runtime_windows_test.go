//go:build windows

package contracts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsBootstrapCancelsStalledResponseBody(t *testing.T) {
	powerShell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		powerShell, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is unavailable")
	}
	root := repositoryRoot(t)
	bootstrap := filepath.Join(root, "bootstrap", "windows.ps1")
	destination := filepath.Join(t.TempDir(), "archive.zip")
	script := strings.ReplaceAll(`
$ErrorActionPreference = "Stop"
$env:MDS_VERSION = "0.1.0"
$env:MDS_SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
$env:MDS_BOOTSTRAP_LIBRARY_ONLY = "1"
. '__BOOTSTRAP__'
Add-Type -AssemblyName System.Net.Http
Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Threading;
using System.Threading.Tasks;

public sealed class StallingStream : Stream
{
    public override bool CanRead { get { return true; } }
    public override bool CanSeek { get { return false; } }
    public override bool CanWrite { get { return false; } }
    public override long Length { get { throw new NotSupportedException(); } }
    public override long Position {
        get { throw new NotSupportedException(); }
        set { throw new NotSupportedException(); }
    }
    public override void Flush() {}
    public override int Read(byte[] buffer, int offset, int count) {
        throw new NotSupportedException();
    }
    public override Task<int> ReadAsync(
        byte[] buffer,
        int offset,
        int count,
        CancellationToken cancellationToken
    ) {
        var completion = new TaskCompletionSource<int>();
        cancellationToken.Register(() => { completion.TrySetCanceled(); });
        return completion.Task;
    }
    public override long Seek(long offset, SeekOrigin origin) {
        throw new NotSupportedException();
    }
    public override void SetLength(long value) {
        throw new NotSupportedException();
    }
    public override void Write(byte[] buffer, int offset, int count) {
        throw new NotSupportedException();
    }
}

public sealed class StallingHandler : HttpMessageHandler
{
    protected override Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken
    ) {
        var response = new HttpResponseMessage(HttpStatusCode.OK);
        response.Content = new StreamContent(new StallingStream());
        return Task.FromResult(response);
    }
}
'@
$timer = [System.Diagnostics.Stopwatch]::StartNew()
try {
    Get-BoundedHttpsFile -Uri "https://example.invalid/archive.zip" -Destination '__DESTINATION__' -Timeout ([TimeSpan]::FromMilliseconds(250)) -Handler ([StallingHandler]::new())
    throw "stalled response body unexpectedly completed"
}
catch {
    $type = $_.Exception.GetType().FullName
    if ($type -notmatch "TaskCanceledException|OperationCanceledException") {
        throw
    }
}
finally {
    $timer.Stop()
}
if ($timer.Elapsed -gt [TimeSpan]::FromSeconds(5)) {
    throw "stalled response body exceeded the cancellation budget"
}
`, "'__BOOTSTRAP__'", "'"+strings.ReplaceAll(bootstrap, "'", "''")+"'")
	script = strings.ReplaceAll(
		script,
		"'__DESTINATION__'",
		"'"+strings.ReplaceAll(destination, "'", "''")+"'",
	)
	scriptPath := filepath.Join(t.TempDir(), "stalled-body.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile(test script): %v", err)
	}
	command := exec.Command(
		powerShell,
		"-NoProfile",
		"-NonInteractive",
		"-File",
		scriptPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf(
			"Windows stalled-body cancellation test failed: %v\n%s",
			err,
			output,
		)
	}
}
