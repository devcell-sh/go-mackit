package vm

import (
	"testing"
)

func TestDiskFormatString(t *testing.T) {
	tests := []struct {
		format DiskFormat
		want   string
	}{
		{DiskFormatDefault, "default"},
		{DiskFormatQcow2, "qcow2"},
		{DiskFormatRaw, "raw"},
	}
	for _, tt := range tests {
		if got := tt.format.String(); got != tt.want {
			t.Errorf("DiskFormat(%d).String() = %q, want %q", tt.format, got, tt.want)
		}
	}
}

func TestResolveBackend(t *testing.T) {
	type dummy struct{ VMBackend }

	reg := map[string]VMBackend{"qemu": dummy{}}

	b, name, err := ResolveBackend("", reg)
	if err != nil {
		t.Fatalf("ResolveBackend default: %v", err)
	}
	if name != "qemu" {
		t.Fatalf("expected default backend qemu, got %q", name)
	}
	if b == nil {
		t.Fatal("backend is nil")
	}

	_, _, err = ResolveBackend("nonexistent", reg)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
