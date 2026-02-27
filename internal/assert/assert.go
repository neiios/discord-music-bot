package assert

import (
	"cmp"
	"fmt"
	"reflect"
	"strings"
	"time"
)

func msg(defaultMsg string, msgAndArgs []any) string {
	if len(msgAndArgs) == 0 {
		return defaultMsg
	}
	if s, ok := msgAndArgs[0].(string); ok {
		if len(msgAndArgs) == 1 {
			return s
		}
		return fmt.Sprintf(s, msgAndArgs[1:]...)
	}
	return fmt.Sprint(msgAndArgs...)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	}
	return false
}

func NoErr(t interface {
	Helper()
	Errorf(string, ...any)
}, err error, msgAndArgs ...any) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: unexpected error: %v", msg("NoErr", msgAndArgs), err)
	}
}

func NoErrf(t interface {
	Helper()
	Fatalf(string, ...any)
}, err error, msgAndArgs ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg("NoErrf", msgAndArgs), err)
	}
}

func IsErr(t interface {
	Helper()
	Errorf(string, ...any)
}, err error, msgAndArgs ...any) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error, got nil", msg("IsErr", msgAndArgs))
	}
}

func ErrContains(t interface {
	Helper()
	Errorf(string, ...any)
}, err error, substr string, msgAndArgs ...any) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error containing %q, got nil", msg("ErrContains", msgAndArgs), substr)
		return
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("%s: error %q does not contain %q", msg("ErrContains", msgAndArgs), err.Error(), substr)
	}
}

func Equal[T comparable](t interface {
	Helper()
	Errorf(string, ...any)
}, got, want T, msgAndArgs ...any) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg("Equal", msgAndArgs), got, want)
	}
}

func Equalf[T comparable](t interface {
	Helper()
	Fatalf(string, ...any)
}, got, want T, msgAndArgs ...any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", msg("Equalf", msgAndArgs), got, want)
	}
}

func NotEqual[T comparable](t interface {
	Helper()
	Errorf(string, ...any)
}, got, notWant T, msgAndArgs ...any) {
	t.Helper()
	if got == notWant {
		t.Errorf("%s: got %v, want anything else", msg("NotEqual", msgAndArgs), got)
	}
}

func DerefEqual[T comparable](t interface {
	Helper()
	Errorf(string, ...any)
	Fatalf(string, ...any)
}, ptr *T, want T, msgAndArgs ...any) {
	t.Helper()
	if ptr == nil {
		t.Fatalf("%s: got nil pointer, want %v", msg("DerefEqual", msgAndArgs), want)
		return
	}
	if *ptr != want {
		t.Errorf("%s: got %v, want %v", msg("DerefEqual", msgAndArgs), *ptr, want)
	}
}

func True(t interface {
	Helper()
	Errorf(string, ...any)
}, v bool, msgAndArgs ...any) {
	t.Helper()
	if !v {
		t.Errorf("%s: got false, want true", msg("True", msgAndArgs))
	}
}

func False(t interface {
	Helper()
	Errorf(string, ...any)
}, v bool, msgAndArgs ...any) {
	t.Helper()
	if v {
		t.Errorf("%s: got true, want false", msg("False", msgAndArgs))
	}
}

func Nil(t interface {
	Helper()
	Errorf(string, ...any)
}, v any, msgAndArgs ...any) {
	t.Helper()
	if !isNil(v) {
		t.Errorf("%s: got %v, want nil", msg("Nil", msgAndArgs), v)
	}
}

func NotNil(t interface {
	Helper()
	Errorf(string, ...any)
}, v any, msgAndArgs ...any) {
	t.Helper()
	if isNil(v) {
		t.Errorf("%s: got nil, want non-nil", msg("NotNil", msgAndArgs))
	}
}

func NotNilf(t interface {
	Helper()
	Fatalf(string, ...any)
}, v any, msgAndArgs ...any) {
	t.Helper()
	if isNil(v) {
		t.Fatalf("%s: got nil, want non-nil", msg("NotNilf", msgAndArgs))
	}
}

func Len[S ~[]E, E any](t interface {
	Helper()
	Errorf(string, ...any)
}, got S, want int, msgAndArgs ...any) {
	t.Helper()
	if len(got) != want {
		t.Errorf("%s: len = %d, want %d", msg("Len", msgAndArgs), len(got), want)
	}
}

func Lenf[S ~[]E, E any](t interface {
	Helper()
	Fatalf(string, ...any)
}, got S, want int, msgAndArgs ...any) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("%s: len = %d, want %d", msg("Lenf", msgAndArgs), len(got), want)
	}
}

func NotEmpty[S ~[]E, E any](t interface {
	Helper()
	Fatalf(string, ...any)
}, got S, msgAndArgs ...any) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: got empty slice, want non-empty", msg("NotEmpty", msgAndArgs))
	}
}

func SlicesEqual[S ~[]E, E comparable](t interface {
	Helper()
	Errorf(string, ...any)
	Fatalf(string, ...any)
}, got, want S, msgAndArgs ...any) {
	t.Helper()
	m := msg("SlicesEqual", msgAndArgs)
	if len(got) != len(want) {
		t.Fatalf("%s: len = %d, want %d", m, len(got), len(want))
		return
	}
	// detect byte slices for hex formatting
	isByte := false
	if len(got) > 0 {
		var zero E
		switch any(zero).(type) {
		case byte:
			isByte = true
		}
	}
	for i := range want {
		if got[i] != want[i] {
			if isByte {
				t.Errorf("%s[%d] = 0x%02X, want 0x%02X", m, i, any(got[i]), any(want[i]))
			} else {
				t.Errorf("%s[%d] = %v, want %v", m, i, got[i], want[i])
			}
		}
	}
}

func Greater[T cmp.Ordered](t interface {
	Helper()
	Errorf(string, ...any)
}, got, threshold T, msgAndArgs ...any) {
	t.Helper()
	if got <= threshold {
		t.Errorf("%s: got %v, want > %v", msg("Greater", msgAndArgs), got, threshold)
	}
}

func Greaterf[T cmp.Ordered](t interface {
	Helper()
	Fatalf(string, ...any)
}, got, threshold T, msgAndArgs ...any) {
	t.Helper()
	if got <= threshold {
		t.Fatalf("%s: got %v, want > %v", msg("Greaterf", msgAndArgs), got, threshold)
	}
}

func GreaterOrEqual[T cmp.Ordered](t interface {
	Helper()
	Errorf(string, ...any)
}, got, threshold T, msgAndArgs ...any) {
	t.Helper()
	if got < threshold {
		t.Errorf("%s: got %v, want >= %v", msg("GreaterOrEqual", msgAndArgs), got, threshold)
	}
}

func Less[T cmp.Ordered](t interface {
	Helper()
	Errorf(string, ...any)
}, got, threshold T, msgAndArgs ...any) {
	t.Helper()
	if got >= threshold {
		t.Errorf("%s: got %v, want < %v", msg("Less", msgAndArgs), got, threshold)
	}
}

func Recv[T any](t interface {
	Helper()
	Fatalf(string, ...any)
}, ch <-chan T, timeout time.Duration, msgAndArgs ...any) T {
	t.Helper()
	select {
	case v, ok := <-ch:
		if !ok {
			t.Fatalf("%s: channel closed, expected value", msg("Recv", msgAndArgs))
		}
		return v
	case <-time.After(timeout):
		t.Fatalf("%s: timed out after %v waiting for channel receive", msg("Recv", msgAndArgs), timeout)
	}
	var zero T
	return zero
}

func ChanClosed[T any](t interface {
	Helper()
	Errorf(string, ...any)
}, ch <-chan T, msgAndArgs ...any) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("%s: channel still open, expected closed", msg("ChanClosed", msgAndArgs))
		}
	default:
		t.Errorf("%s: channel blocked, expected closed", msg("ChanClosed", msgAndArgs))
	}
}

func ChanOpen[T any](t interface {
	Helper()
	Fatalf(string, ...any)
}, ch <-chan T, msgAndArgs ...any) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("%s: received unexpected value from channel", msg("ChanOpen", msgAndArgs))
		} else {
			t.Fatalf("%s: channel closed, expected open", msg("ChanOpen", msgAndArgs))
		}
	default:
		// channel is open and blocking - correct
	}
}
