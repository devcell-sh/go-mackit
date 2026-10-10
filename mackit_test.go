package mackit

import (
	"context"
	"strings"
	"testing"

	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/build/buildopts"
)

func TestBuild_RequiresDest(t *testing.T) {
	err := Build(context.Background(), build.Config{
		InstallerDMG: "base.dmg",
	})
	if err == nil || !strings.Contains(err.Error(), "Dest") {
		t.Fatalf("expected Dest error, got %v", err)
	}
}

func TestBuild_RequiresMedia(t *testing.T) {
	err := Build(context.Background(), build.Config{Dest: "out.qcow2"})
	if err == nil || !strings.Contains(err.Error(), "InstallerDMG") {
		t.Fatalf("expected InstallerDMG error, got %v", err)
	}
}

func TestBuild_NilOptsGetsDefaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Build(ctx, build.Config{
		Dest:         t.TempDir() + "/out.qcow2",
		CacheDir:     t.TempDir(),
		InstallerDMG: "/nonexistent/base.dmg",
	})
	if err == nil {
		t.Fatal("expected error for canceled build with nonexistent media")
	}
	if strings.Contains(err.Error(), "nil pointer") {
		t.Fatalf("nil Opts dereferenced: %v", err)
	}
}

func TestBuild_RecoveryDispatch(t *testing.T) {
	err := Build(context.Background(), build.Config{
		Dest:         t.TempDir() + "/out.qcow2",
		InstallerDMG: "base.dmg",
		Opts: &buildopts.BuildOpts{
			Recovery: true,
		},
	})
	if err == nil {
		t.Fatal("expected error with nonexistent media")
	}
	// Recovery mode should attempt to validate the media and fail.
	if !strings.Contains(err.Error(), "installer media") && !strings.Contains(err.Error(), "base.dmg") {
		t.Fatalf("expected media validation error, got %v", err)
	}
}
