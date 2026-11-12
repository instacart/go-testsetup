package testsetup

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/instacart/go-testhooks/testhooks"
)

type testContext struct {
	t          *testing.T
	generation uint64
}

var (
	mu                 sync.Mutex
	setMetadataPointer atomic.Bool
	tmpMetadataPointer any
	currentCtx         atomic.Pointer[testContext]
	generation         atomic.Uint64
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
	if ctx := currentCtx.Load(); ctx != nil && ctx.t != nil {
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
	if ctx := currentCtx.Load(); ctx != nil && ctx.t != nil {
		mu.Unlock()
		FatalIfError(t, fmt.Errorf("RunSetup called twice or using parallel tests which is unsupported"))
	}
	generation.Add(1)
	currentCtx.Store(&testContext{t: t, generation: generation.Load()})
	letResets = nil
	funcs := append([]func(*testing.T){}, setupFuncs...)
	mu.Unlock()

	// Register this before any setup callback. LIFO cleanup ordering then runs
	// it after teardown callbacks registered by setup or by the test body, so
	// teardowns still see the cached values. The context is cleared here, so
	// Lets cannot be used after this cleanup starts.
	t.Cleanup(func() {
		mu.Lock()
		resets := letResets
		letResets = nil
		generation.Add(1)
		currentCtx.Store(nil)
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
	ctx := currentCtx.Load()
	started := ctx != nil && ctx.t != nil
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

type letState[A any] struct {
	generation uint64
	status     int32
	value      A
}

// letMetadata contains the state of a LetWrapper.
type letMetadata[A any] struct {
	// definitionT *testing.T
	constructor func(t *testing.T) A
	state       atomic.Pointer[letState[A]]
	buildMu     sync.Mutex
}

// createWrapper creates a LetWrapper from the metadata.
func (m *letMetadata[A]) createWrapper(definitionT *testing.T) LetWrapper[A] {
	definitionT.Helper()
	m.state.Store(&letState[A]{status: letStatusInitial})
	return LetWrapper[A](func() (res A) {
		if setMetadataPointer.Load() {
			*(tmpMetadataPointer.(**letMetadata[A])) = m
			return
		}

		ctx := currentCtx.Load()
		if ctx == nil || ctx.t == nil {
			definitionT.Helper()
			// Note: we cannot call FatalIfError because definitionT
			// is likely a parent test and we may be within a subtest.
			// Calling Fatal/FailNow methods causes the test framework to
			// get really confused and the IDE error reporting sends you
			// to random non existing files if you do that. So instead
			// we just mark the failure and let the subtest continue.
			//
			// The context is nil before RunSetup and after the RunSetup
			// cleanup runs, so both cases are rejected here.
			errorfFuncHook.Apply(definitionT.Errorf)("ERROR: %v", fmt.Errorf("Must call RunSetup before using Let"))
			return
		}
		t := ctx.t
		t.Helper()

		state := m.loadState(ctx.generation)
		switch state.status {
		case letStatusBuilt:
			return state.value
		case letStatusFailed:
			FatalIfError(t, fmt.Errorf("Let failed to build"))
			return state.value
		default:
			return m.construct(t, ctx.generation, state)
		}
	})
}

// loadState loads the current state and verifies that it belongs to the
// expected RunSetup generation. An uninitialized state (generation zero) is
// allowed; construct stamps it with the active generation.
func (m *letMetadata[A]) loadState(expectedGeneration uint64) *letState[A] {
	state := m.state.Load()
	if state.status == letStatusInitial && state.generation == 0 {
		// An uninitialized state is not tied to a generation yet. Only allow
		// claiming it while expectedGeneration is the active generation; a swap
		// that reset it leaves it uninitialized for the next generation.
		if !m.generationMatches(expectedGeneration) {
			panic("testsetup: the test ended before evaluation finished")
		}
		return state
	}
	if state.generation != expectedGeneration {
		panic("testsetup: Let state belongs to a different RunSetup generation; the test ended before evaluation finished")
	}
	return state
}

// construct builds the value for state. buildMu serializes constructors; after
// acquiring it we reload and re-check both the generation and the state.
func (m *letMetadata[A]) construct(t *testing.T, expectedGeneration uint64, state *letState[A]) A {
	m.buildMu.Lock()
	defer m.buildMu.Unlock()

	state = m.loadState(expectedGeneration)
	switch state.status {
	case letStatusBuilt:
		return state.value
	case letStatusFailed:
		FatalIfError(t, fmt.Errorf("Let failed to build"))
		return state.value
	case letStatusInitial:
		building := &letState[A]{generation: expectedGeneration, status: letStatusBuilding}
		if !m.state.CompareAndSwap(state, building) {
			panic("testsetup: Let state changed while starting construction")
		}
		state = building
	}

	registerLetReset(func() {
		m.state.Store(&letState[A]{status: letStatusInitial})
	})

	built := false
	defer func() {
		if built {
			return
		}
		// If the test ended and cleanup reset the state, this CAS fails and
		// the reset wins. Otherwise the failed state belongs to this
		// generation and a later evaluation will detect the generation change.
		m.state.CompareAndSwap(state, &letState[A]{generation: expectedGeneration, status: letStatusFailed})
	}()

	value := m.constructor(t)
	if !m.state.CompareAndSwap(state, &letState[A]{generation: expectedGeneration, status: letStatusBuilt, value: value}) {
		panic("testsetup: Let was reset while constructing")
	}
	built = true
	return value
}

func (m *letMetadata[A]) generationMatches(expectedGeneration uint64) bool {
	ctx := currentCtx.Load()
	return ctx != nil && ctx.t != nil && ctx.generation == expectedGeneration
}

// Let creates a LetWrapper that lazily loads the value when requested for
// by calling the provider constructor.
//
// A Let can only be evaluated after RunSetup starts and before the RunSetup
// lifecycle cleanup runs. Calls before RunSetup or after that cleanup fail.
//
// Each evaluation is bound to the RunSetup generation it started in. If the
// test ends before evaluation finishes, evaluation panics. Concurrent
// evaluation is supported. Recursive construction (a Let whose constructor
// evaluates itself) deadlocks.
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
