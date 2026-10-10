// Package logx holds slog helpers shared by mackit's packages.
//
// Every record mackit emits carries a "service" attribute naming the
// component that produced it, so a build.jsonl line can be attributed:
// "mackit" for mackit's own code, "textshot" for the OCR engine, and for
// guest console output forwarded from the serial port "uefi", "opencore"
// or "xnu" (see vm/qemu.TailSerial).
package logx

import (
	"context"
	"log/slog"
)

// ServiceKey is the attribute key naming the emitting component.
const ServiceKey = "service"

// ServiceMackit tags records produced by mackit itself.
const ServiceMackit = "mackit"

// WithService wraps h so that records lacking a "service" attribute get
// service=name. Records or loggers that already carry one (for example a
// serial-port line tagged "xnu") keep theirs.
func WithService(h slog.Handler, name string) slog.Handler {
	if sh, ok := h.(*serviceHandler); ok {
		h = sh.next
	}
	return &serviceHandler{next: h, service: name}
}

// Logger returns l with a default service attribute, see WithService.
func Logger(l *slog.Logger, name string) *slog.Logger {
	if l == nil {
		return slog.New(WithService(slog.DiscardHandler, name))
	}
	return slog.New(WithService(l.Handler(), name))
}

type serviceHandler struct {
	next    slog.Handler
	service string
	// tagged is set once WithAttrs supplied a service, so Handle need not
	// add one.
	tagged bool
}

func (h *serviceHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *serviceHandler) Handle(ctx context.Context, r slog.Record) error {
	if !h.tagged && !hasService(r) {
		r.AddAttrs(slog.String(ServiceKey, h.service))
	}
	return h.next.Handle(ctx, r)
}

func (h *serviceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	tagged := h.tagged
	for _, a := range attrs {
		if a.Key == ServiceKey {
			tagged = true
		}
	}
	return &serviceHandler{next: h.next.WithAttrs(attrs), service: h.service, tagged: tagged}
}

func (h *serviceHandler) WithGroup(name string) slog.Handler {
	// Inside a group the service attribute would be nested; keep tagging
	// at the top level by marking the group handler as already tagged
	// only if it was. Groups are not used by mackit.
	return &serviceHandler{next: h.next.WithGroup(name), service: h.service, tagged: h.tagged}
}

func hasService(r slog.Record) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == ServiceKey {
			found = true
			return false
		}
		return true
	})
	return found
}
