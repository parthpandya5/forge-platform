package build

import (
	"strings"
	"sync"
	"testing"
)

func TestImageTagIsImmutableByDeployment(t *testing.T) {
	builder := NewDocker("docker", "", "registry.local:5000", "forge")
	got := builder.ImageTag("demo-api", "018f1111-2222-7333-8444-555555555555")
	want := "registry.local:5000/forge/demo-api:018f1111-2222-7333-8444-555555555555"
	if got != want {
		t.Fatalf("ImageTag() = %q, want %q", got, want)
	}
}

func TestRelayDrainsOversizedOutputLines(t *testing.T) {
	longLine := strings.Repeat("x", 40*1024)
	var chunks []string
	var wait sync.WaitGroup
	result := make(chan error, 1)
	wait.Add(1)
	go relay(strings.NewReader(longLine+"\nend\n"), func(line string) { chunks = append(chunks, line) }, &wait, result)
	wait.Wait()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(chunks, ""); got != longLine+"end" {
		t.Fatalf("relayed %d bytes, want %d", len(got), len(longLine)+3)
	}
}

func TestBuildFailureClassification(t *testing.T) {
	for _, output := range []string{
		"Dockerfile parse error line 3: unknown instruction: FROOM",
		"failed to solve: failed to calculate checksum: /missing: not found",
	} {
		if !isPermanentBuildOutput(output) {
			t.Fatalf("expected permanent classification for %q", output)
		}
	}
	if isPermanentBuildOutput("failed to resolve source metadata: connection reset by peer") {
		t.Fatal("transient registry failure was classified as permanent")
	}
}
