package assert

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// mockT records calls to Errorf/Fatalf without stopping execution.
type mockT struct {
	errors []string
	fatals []string
}

func (m *mockT) Helper() {}
func (m *mockT) Errorf(format string, args ...any) {
	m.errors = append(m.errors, fmt.Sprintf(format, args...))
}
func (m *mockT) Fatalf(format string, args ...any) {
	m.fatals = append(m.fatals, fmt.Sprintf(format, args...))
}

func expectError(t *testing.T, m *mockT) {
	t.Helper()
	if len(m.errors) == 0 {
		t.Error("expected Errorf call, got none")
	}
}

func expectNoError(t *testing.T, m *mockT) {
	t.Helper()
	if len(m.errors) > 0 {
		t.Errorf("unexpected Errorf calls: %v", m.errors)
	}
}

func expectFatal(t *testing.T, m *mockT) {
	t.Helper()
	if len(m.fatals) == 0 {
		t.Error("expected Fatalf call, got none")
	}
}

func expectNoFatal(t *testing.T, m *mockT) {
	t.Helper()
	if len(m.fatals) > 0 {
		t.Errorf("unexpected Fatalf calls: %v", m.fatals)
	}
}

func expectClean(t *testing.T, m *mockT) {
	t.Helper()
	expectNoError(t, m)
	expectNoFatal(t, m)
}

func TestMsg(t *testing.T) {
	if got := msg("default", nil); got != "default" {
		t.Errorf("msg() = %q, want %q", got, "default")
	}
	if got := msg("default", []any{"custom"}); got != "custom" {
		t.Errorf("msg() = %q, want %q", got, "custom")
	}
	if got := msg("default", []any{"x=%d", 42}); got != "x=42" {
		t.Errorf("msg() = %q, want %q", got, "x=42")
	}
	if got := msg("default", []any{123}); got != "123" {
		t.Errorf("msg() = %q, want %q", got, "123")
	}
}

func TestIsNil(t *testing.T) {
	if !isNil(nil) {
		t.Error("isNil(nil) = false")
	}
	var p *int
	if !isNil(p) {
		t.Error("isNil(typed nil *int) = false")
	}
	x := 42
	if isNil(&x) {
		t.Error("isNil(non-nil *int) = true")
	}
	if isNil(42) {
		t.Error("isNil(42) = true")
	}
}

func TestNoErr(t *testing.T) {
	m := &mockT{}
	NoErr(m, nil)
	expectClean(t, m)

	m = &mockT{}
	NoErr(m, errors.New("boom"), "context")
	expectError(t, m)
}

func TestNoErrf(t *testing.T) {
	m := &mockT{}
	NoErrf(m, nil)
	expectClean(t, m)

	m = &mockT{}
	NoErrf(m, errors.New("boom"))
	expectFatal(t, m)
}

func TestIsErr(t *testing.T) {
	m := &mockT{}
	IsErr(m, errors.New("boom"))
	expectClean(t, m)

	m = &mockT{}
	IsErr(m, nil)
	expectError(t, m)
}

func TestErrContains(t *testing.T) {
	m := &mockT{}
	ErrContains(m, errors.New("connection refused"), "refused")
	expectClean(t, m)

	m = &mockT{}
	ErrContains(m, nil, "refused")
	expectError(t, m)

	m = &mockT{}
	ErrContains(m, errors.New("timeout"), "refused")
	expectError(t, m)
}

func TestEqual(t *testing.T) {
	m := &mockT{}
	Equal(m, "hello", "hello")
	Equal(m, 42, 42)
	Equal(m, byte(0xFF), byte(0xFF))
	expectClean(t, m)

	type point struct{ X, Y int }
	m = &mockT{}
	Equal(m, point{1, 2}, point{1, 2})
	expectClean(t, m)

	m = &mockT{}
	Equal(m, "a", "b")
	expectError(t, m)
}

func TestEqualf(t *testing.T) {
	m := &mockT{}
	Equalf(m, 1, 1)
	expectClean(t, m)

	m = &mockT{}
	Equalf(m, 1, 2)
	expectFatal(t, m)
}

func TestNotEqual(t *testing.T) {
	m := &mockT{}
	NotEqual(m, 1, 2)
	expectClean(t, m)

	m = &mockT{}
	NotEqual(m, 1, 1)
	expectError(t, m)
}

func TestDerefEqual(t *testing.T) {
	m := &mockT{}
	v := 42
	DerefEqual(m, &v, 42, "value")
	expectClean(t, m)

	m = &mockT{}
	DerefEqual(m, (*int)(nil), 42)
	expectFatal(t, m)

	m = &mockT{}
	v = 99
	DerefEqual(m, &v, 42, "value")
	expectNoFatal(t, m)
	expectError(t, m)
}

func TestTrueFalse(t *testing.T) {
	m := &mockT{}
	True(m, true)
	False(m, false)
	expectClean(t, m)

	m = &mockT{}
	True(m, false)
	expectError(t, m)

	m = &mockT{}
	False(m, true)
	expectError(t, m)
}

func TestNil(t *testing.T) {
	m := &mockT{}
	Nil(m, nil)
	expectClean(t, m)

	var p *int
	m = &mockT{}
	Nil(m, p)
	expectClean(t, m)

	m = &mockT{}
	x := 1
	Nil(m, &x)
	expectError(t, m)
}

func TestNotNil(t *testing.T) {
	m := &mockT{}
	x := 1
	NotNil(m, &x)
	expectClean(t, m)

	m = &mockT{}
	NotNil(m, nil)
	expectError(t, m)
}

func TestNotNilf(t *testing.T) {
	m := &mockT{}
	x := 1
	NotNilf(m, &x)
	expectClean(t, m)

	m = &mockT{}
	NotNilf(m, nil)
	expectFatal(t, m)
}

func TestLen(t *testing.T) {
	m := &mockT{}
	Len(m, []int{1, 2, 3}, 3)
	expectClean(t, m)

	m = &mockT{}
	Len(m, []int{1}, 5)
	expectError(t, m)
}

func TestLenf(t *testing.T) {
	m := &mockT{}
	Lenf(m, []string{"a", "b"}, 2)
	expectClean(t, m)

	m = &mockT{}
	Lenf(m, []string{}, 1)
	expectFatal(t, m)
}

func TestNotEmpty(t *testing.T) {
	m := &mockT{}
	NotEmpty(m, []int{1})
	expectClean(t, m)

	m = &mockT{}
	NotEmpty(m, []int{})
	expectFatal(t, m)
}

func TestSlicesEqual(t *testing.T) {
	m := &mockT{}
	SlicesEqual(m, []int{1, 2, 3}, []int{1, 2, 3})
	expectClean(t, m)

	m = &mockT{}
	SlicesEqual(m, []int{1}, []int{1, 2})
	expectFatal(t, m)

	m = &mockT{}
	SlicesEqual(m, []int{1, 9, 3}, []int{1, 2, 3})
	expectNoFatal(t, m)
	expectError(t, m)

	m = &mockT{}
	SlicesEqual(m, []byte{0xAA, 0xBB}, []byte{0xAA, 0xCC}, "frame")
	expectError(t, m)
	if len(m.errors) > 0 {
		got := m.errors[0]
		if !contains(got, "0xBB") || !contains(got, "0xCC") {
			t.Errorf("expected hex in error message, got: %s", got)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGreater(t *testing.T) {
	m := &mockT{}
	Greater(m, 10, 5)
	expectClean(t, m)

	m = &mockT{}
	Greater(m, 5, 5)
	expectError(t, m)

	m = &mockT{}
	Greater(m, 3, 5)
	expectError(t, m)
}

func TestGreaterf(t *testing.T) {
	m := &mockT{}
	Greaterf(m, 10, 5)
	expectClean(t, m)

	m = &mockT{}
	Greaterf(m, 5, 5)
	expectFatal(t, m)
}

func TestGreaterOrEqual(t *testing.T) {
	m := &mockT{}
	GreaterOrEqual(m, 5, 5)
	GreaterOrEqual(m, 6, 5)
	expectClean(t, m)

	m = &mockT{}
	GreaterOrEqual(m, 4, 5)
	expectError(t, m)
}

func TestLess(t *testing.T) {
	m := &mockT{}
	Less(m, 3, 5)
	expectClean(t, m)

	m = &mockT{}
	Less(m, 5, 5)
	expectError(t, m)

	m = &mockT{}
	Less(m, time.Millisecond, time.Second)
	expectClean(t, m)
}

func TestRecv(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 42
	m := &mockT{}
	got := Recv(m, ch, 100*time.Millisecond, "value")
	expectClean(t, m)
	if got != 42 {
		t.Errorf("Recv = %d, want 42", got)
	}

	empty := make(chan int)
	m = &mockT{}
	Recv(m, empty, time.Millisecond)
	expectFatal(t, m)
}

func TestRecvClosed(t *testing.T) {
	ch := make(chan int)
	close(ch)
	m := &mockT{}
	Recv(m, ch, 100*time.Millisecond)
	expectFatal(t, m)
}

func TestChanClosed(t *testing.T) {
	ch := make(chan int)
	close(ch)
	m := &mockT{}
	ChanClosed(m, ch)
	expectClean(t, m)

	open := make(chan int)
	m = &mockT{}
	ChanClosed(m, open)
	expectError(t, m)
}

func TestChanOpen(t *testing.T) {
	ch := make(chan int)
	m := &mockT{}
	ChanOpen(m, ch)
	expectClean(t, m)

	closed := make(chan int)
	close(closed)
	m = &mockT{}
	ChanOpen(m, closed)
	expectFatal(t, m)
}
