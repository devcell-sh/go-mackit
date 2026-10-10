package vm

import (
	"fmt"
	"os"
)

func ResolveBackend(name string, registry map[string]VMBackend) (VMBackend, string, error) {
	if name == "" {
		name = os.Getenv("MACKIT_VM_BACKEND")
	}
	if name == "" {
		name = defaultBackendName
	}
	b, ok := registry[name]
	if !ok {
		return nil, name, fmt.Errorf("unknown VM backend %q", name)
	}
	return b, name, nil
}

func DefaultBackendName() string {
	return defaultBackendName
}
