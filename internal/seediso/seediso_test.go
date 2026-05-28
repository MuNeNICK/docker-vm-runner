package seediso

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/munenick/docker-vm-runner/internal/process"
)

func TestBuilderWritesSeedFilesAndRunsGenISO(t *testing.T) {
	runner := &fakeRunner{}
	builder := NewBuilder(runner)
	outputPath := filepath.Join(t.TempDir(), "seed.iso")

	err := builder.Build(context.Background(), outputPath, SeedContent{
		MetaData:   "instance-id: iid-test\n",
		UserData:   "#cloud-config\n",
		VendorData: "vendor\n",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	for _, name := range []string{"meta-data", "user-data", "vendor-data"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(outputPath), name)); err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	cmd := runner.commands[0]
	if cmd.Name != "genisoimage" || !strings.Contains(strings.Join(cmd.Args, " "), outputPath) {
		t.Fatalf("command = %#v", cmd)
	}
}

type fakeRunner struct {
	commands []process.Command
}

func (r *fakeRunner) Run(_ context.Context, cmd process.Command) (process.Result, error) {
	r.commands = append(r.commands, cmd)
	return process.Result{}, nil
}
