package testsetup

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/instacart/go-testhooks/testhooks"
)

var (
	mu                 sync.Mutex
	setMetadataPointer atomic.Bool
	tmpMetadataPointer any
	currentT           *testing.T
	setupFuncs         []func(t *testing.T)
	letResets          []func()
)

var (
	fatalfFuncHook = testhooks.NewValueHook[func(format string, args ...any)]()
	errorfFuncHook = testhooks.NewValueHook[func(format string, args ...any)]()
)

// FatalIfError is a helper function that calls t.Fatalf if err is not nil.
//
// It also formats the error with "%+v" to include the stack traces.
func FatalIfError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		fatalfFuncHook.Apply(t.Fatalf)("ERROR: %+v", err)
	}
}

func FatalIfErrorElseReturn[T any](t *testing.T, err error, val T) T {
	t.Helper()
	FatalIfError(t, err)
	return val
}

// AssertFatalContains is a helper to run a block of code with FatailIfError
// being mocked to panic with the given message so we can actually assert on it.
//
// This is useful for testing test utilities.
func AssertFatalContains(t *testing.T, f func(), contains string) {
	t.Helper()
	defer func() {
		t.Helper()
		if r := recover(); r != nil {
			assert.Contains(t, r, contains)
		}
	}()
	fatalfFuncHook.WithValue(func(format string, args ...any) {
		t.Helper()
		panic(fmt.Sprintf(format, args...))
	}).RunFunc(f)
	t.Errorf("Expected a panic")
}

// Add adds a setup function to the list of setup functions to be run when
// `testsetup.RunSetup` is called.
//
// It must be called before `testsetup.RunSetup`.
//
// Functions added are removed after the current test finishes using `t.Cleanup`.
// This ensures that setup functions added only affect the current test and its children.
func AddSetup(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if currentT != nil {
		FatalIfError(t, fmt.Errorf("AddSetup must be called before RunSetup"))
	}
	setupFuncs = append(setupFuncs, f)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		setupFuncs = setupFuncs[:len(setupFuncs)-1]
	})
}

// RunSetup runs setup functions added by `testsetup.AddSetup`.
//
// Setup callbacks must not call ModifyLet or ModifyLetValue; those helpers must
// run before setup starts.
//
// A Let can only be evaluated after RunSetup starts and before its lifecycle
// cleanup runs. Cached Let values are reset by that cleanup, after teardown
// callbacks registered by setup functions or the test body.
func RunSetup(t *testing.T) {
	t.Helper()
	mu.Lock()
	if currentT != nil {
		mu.Unlock()
		FatalIfError(t, fmt.Errorf("RunSetup called twice or using parallel tests which is unsupported"))
	}
	currentT = t
	letResets = nil
	funcs := append([]func(*testing.T){}, setupFuncs...)
	mu.Unlock()

	// Register this before any setup callback. LIFO cleanup ordering then runs
	// it after teardown callbacks registered by setup or by the test body, so
	// teardowns still see the cached values. currentT is cleared here, so Lets
	// cannot be used after this cleanup starts.
	t.Cleanup(func() {
		mu.Lock()
		resets := letResets
		letResets = nil
		currentT = nil
		mu.Unlock()

		for i := len(resets) - 1; i >= 0; i-- {
			resets[i]()
		}
	})

	// Run callbacks without holding mu: they may call public helpers that
	// need it themselves.
	for _, f := range funcs {
		f(t)
	}
}

// LetWrapper represents a value that is lazy loaded when requested for.
//
// See `testsetup.Let` and `testsetup.LetValue` for creating instances of `LetWrapper`.
type LetWrapper[A any] func() A

// Eager forces the value to be loaded eagerly for the current test.
//
// Shorthand for:
//
//	AddSetup(t, func(t *testing.T) { letWrapper() })
func (l LetWrapper[A]) Eager(t *testing.T) LetWrapper[A] {
	AddSetup(t, func(t *testing.T) { l() })
	return l
}

// registerLetReset records a reset for the active RunSetup lifecycle. The
// lifecycle cleanup runs the resets after teardown callbacks registered by
// setup or the test body.
func registerLetReset(reset func()) {
	mu.Lock()
	defer mu.Unlock()
	letResets = append(letResets, reset)
}

// ensureBeforeRunSetup fails the test when RunSetup has already started.
func ensureBeforeRunSetup(t *testing.T, name string) {
	t.Helper()
	mu.Lock()
	started := currentT != nil
	mu.Unlock()
	if started {
		FatalIfError(t, fmt.Errorf("%s must be called before RunSetup", name))
	}
}

// ModifyLet modifies the value of the LetWrapper for the current test.
//
// It receives a function that can access the previous constructor through the `super` function.
//
// It must be called before RunSetup. Once setup starts, Let values may already
// be built, so calling ModifyLet (including from a setup function) fails the
// test instead of silently keeping a stale cached value.
func ModifyLet[A any](t *testing.T, l LetWrapper[A], f func(t *testing.T, super func() A) A) {
	t.Helper()
	ensureBeforeRunSetup(t, "ModifyLet")
	modifyLet(t, l, f)
}

// ModifyLetValue is a variant of ModifyLet that uses a constant value.
//
// It must be called before RunSetup; see ModifyLet.
func ModifyLetValue[A immutable](t *testing.T, l LetWrapper[A], a A) {
	t.Helper()
	ensureBeforeRunSetup(t, "ModifyLetValue")
	modifyLet(t, l, func(t *testing.T, super func() A) A {
		return a
	})
}

func modifyLet[A any](t *testing.T, l LetWrapper[A], f func(t *testing.T, super func() A) A) {
	t.Helper()
	m := l.metadata(t)
	previousConstructor := m.constructor
	t.Cleanup(func() {
		m.constructor = previousConstructor
	})
	m.constructor = func(t *testing.T) A {
		return f(t, func() A { return previousConstructor(t) })
	}
}

// metadata returns the letMetadata for the LetWrapper.
//
// Because golang functions are not comparable, this uses global
// state to retrieve the metadata pointer.
func (l LetWrapper[A]) metadata(t *testing.T) (res *letMetadata[A]) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	tmpMetadataPointer = &res
	setMetadataPointer.Store(true)
	defer func() { setMetadataPointer.Store(false) }()
	l()
	tmpMetadataPointer = nil
	if res == nil {
		FatalIfError(t, fmt.Errorf("Could not find metadata for Let!"))
	}
	return
}

const (
	letStatusInitial = int32(iota)
	letStatusBuilding
	letStatusBuilt
	letStatusFailed
)

// letMetadata contains the state of a LetWrapper.
type letMetadata[A any] struct {
	// definitionT *testing.T
	constructor func(t *testing.T) A
	status      atomic.Int32
	value       A
}

// createWrapper creates a LetWrapper from the metadata.
func (m *letMetadata[A]) createWrapper(definitionT *testing.T) LetWrapper[A] {
	definitionT.Helper()
	return LetWrapper[A](func() (res A) {
		if setMetadataPointer.Load() {
			*(tmpMetadataPointer.(**letMetadata[A])) = m
			return
		}

		mu.Lock()
		t := currentT
		mu.Unlock()
		if t == nil {
			definitionT.Helper()
			// Note: we cannot call FatalIfError because definitionT
			// is likely a parent test and we may be within a subtest.
			// Calling Fatal/FailNow methods causes the test framework to
			// get really confused and the IDE error reporting sends you
			// to random non existing files if you do that. So instead
			// we just mark the failure and let the subtest continue.
			//
			// currentT is nil before RunSetup and after the RunSetup
			// cleanup runs, so both cases are rejected here.
			errorfFuncHook.Apply(definitionT.Errorf)("ERROR: %v", fmt.Errorf("Must call RunSetup before using Let"))
			return
		}
		t.Helper()

		for {
			// Reload on every iteration: another goroutine may have won the
			// compare-and-swap or finished building while we were waiting.
			status := m.status.Load()
			if status == letStatusBuilt {
				break
			} else if status == letStatusFailed {
				FatalIfError(t, fmt.Errorf("Let failed to build"))
			} else if status == letStatusBuilding {
				// Recursive construction and concurrent evaluation cannot be
				// distinguished without goroutine identity. Both are unsupported;
				// fail instead of waiting (which would deadlock on a cycle).
				FatalIfError(t, fmt.Errorf("Failed to build Let, possible cycle! Concurrent evaluation from multiple goroutines is also unsupported."))
			} else if status == letStatusInitial {
				if m.status.CompareAndSwap(letStatusInitial, letStatusBuilding) {
					// Register the reset with the RunSetup lifecycle instead of
					// t.Cleanup: a cleanup registered at build time can run before
					// teardowns that were registered earlier, which would let a
					// teardown rebuild and close a replacement. The lifecycle
					// cleanup is registered before setup callbacks, so it runs after
					// those teardowns.
					registerLetReset(func() {
						m.status.Store(letStatusInitial)
						var zeroValue A
						m.value = zeroValue
					})
					defer func() {
						// Try setting status to failure
						m.status.CompareAndSwap(letStatusBuilding, letStatusFailed)
					}()
					m.value = m.constructor(t)
					m.status.Store(letStatusBuilt)
					break
				}
			}
		}
		return m.value
	})
}

// Let creates a LetWrapper that lazily loads the value when requested for
// by calling the provider constructor.
//
// A Let can only be evaluated after RunSetup starts and before the RunSetup
// lifecycle cleanup runs. Calls before RunSetup or after that cleanup fail.
//
// Construction must happen on the goroutine that called RunSetup. Evaluate a
// Let before starting worker goroutines; concurrent construction is unsupported
// and fails the test.
func Let[A any](t *testing.T, constructor func(t *testing.T) A) LetWrapper[A] {
	t.Helper()
	return (&letMetadata[A]{
		constructor: constructor,
		// definitionT: t,
	}).createWrapper(t)
}

// LetValue is a shorthand for Let that uses a constant value.
func LetValue[A immutable](t *testing.T, x A) LetWrapper[A] {
	t.Helper()
	return Let(t, func(t *testing.T) A {
		return x
	})
}

type immutable interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr | ~float32 | ~float64 | ~string | ~bool
}
