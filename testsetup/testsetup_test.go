package testsetup

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type Sample struct{ value string }

func TestTestSetup(t *testing.T) {
	t.Run("Run", func(t *testing.T) {
		events := []string{}
		addEvent := func(event string) {
			events = append(events, event)
		}
		setUp := func(t *testing.T) {
			t.Cleanup(func() {
				events = []string{}
			})
		}

		t.Run("CallsSetupAdded", func(t *testing.T) {
			setUp(t)
			AddSetup(t, func(t *testing.T) {
				addEvent("setup1")
			})
			AddSetup(t, func(t *testing.T) {
				addEvent("setup2")
			})

			RunSetup(t)
			assert.Equal(t, []string{"setup1", "setup2"}, events)
		})

		t.Run("WhenAddIsCalledAfter", func(t *testing.T) {
			setUp(t)
			RunSetup(t)

			AssertFatalContains(t, func() {
				AddSetup(t, func(t *testing.T) { addEvent("setup1") })
			}, "AddSetup must be called before RunSetup")

			assert.Equal(t, []string{}, events)
		})

		t.Run("CallsSetupAddedChildren", func(t *testing.T) {
			setUp(t)
			AddSetup(t, func(t *testing.T) {
				addEvent("setup1")
			})

			t.Run("Child1", func(t *testing.T) {
				AddSetup(t, func(t *testing.T) {
					addEvent("setup2")
				})

				t.Run("Grandchild1", func(t *testing.T) {
					setUp(t)

					RunSetup(t)

					assert.Equal(t, []string{"setup1", "setup2"}, events)
				})

				AddSetup(t, func(t *testing.T) {
					addEvent("setup3")
				})

				t.Run("Grandchild3", func(t *testing.T) {
					setUp(t)

					RunSetup(t)

					assert.Equal(t, []string{"setup1", "setup2", "setup3"}, events)
				})
			})

			t.Run("Child2", func(t *testing.T) {
				setUp(t)

				RunSetup(t)

				assert.Equal(t, []string{"setup1"}, events)
			})
		})

		t.Run("WhenCalledTwice", func(t *testing.T) {
			RunSetup(t)

			AssertFatalContains(t, func() {
				RunSetup(t)
			}, "RunSetup called twice or using parallel tests which is unsupported")
		})
	})

	t.Run("Let", func(t *testing.T) {
		t.Run("Success", func(t *testing.T) {
			a := LetValue(t, 1)

			RunSetup(t)

			assert.Equal(t, 1, a())
		})

		t.Run("LazyByDefault", func(t *testing.T) {
			callCount := 0
			a := Let(t, func(t *testing.T) int {
				callCount++
				return callCount
			})

			RunSetup(t)

			assert.Equal(t, 0, callCount)
			assert.Equal(t, 1, a())
			assert.Equal(t, 1, callCount)
			assert.Equal(t, 1, a())
			assert.Equal(t, 1, callCount)
		})

		t.Run("WhenEager", func(t *testing.T) {
			callCount := 0
			a := Let(t, func(t *testing.T) int {
				callCount++
				return callCount
			}).Eager(t)

			RunSetup(t)

			assert.Equal(t, 1, callCount)
			assert.Equal(t, 1, a())
			assert.Equal(t, 1, callCount)
			assert.Equal(t, 1, a())
			assert.Equal(t, 1, callCount)
		})

		t.Run("OnFailure", func(t *testing.T) {
			a := Let(t, func(t *testing.T) *Sample { panic("failed") })

			RunSetup(t)

			assert.PanicsWithValue(t, "failed", func() { a() })
			AssertFatalContains(t, func() {
				a()
			}, "Let failed to build")
		})

		t.Run("ConcurrentEvaluation", func(t *testing.T) {
			callCount := 0
			start := make(chan struct{})
			a := Let(t, func(t *testing.T) *Sample {
				<-start
				time.Sleep(10 * time.Millisecond)
				callCount++
				return &Sample{value: "value"}
			})
			RunSetup(t)

			const workers = 8
			results := make([]string, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					results[i] = a().value
				}(i)
			}
			close(start)
			wg.Wait()

			for _, result := range results {
				assert.Equal(t, "value", result)
			}
			assert.Equal(t, 1, callCount)
		})

		t.Run("MutationOnChildGetsRestored", func(t *testing.T) {
			a := Let(t, func(t *testing.T) *Sample { return &Sample{value: "original"} })

			t.Run("Child1", func(t *testing.T) {
				RunSetup(t)

				assert.Equal(t, "original", a().value)
				a().value = "changed"
				assert.Equal(t, "changed", a().value)
			})

			t.Run("Child2", func(t *testing.T) {
				RunSetup(t)

				assert.Equal(t, "original", a().value)
			})
		})

		t.Run("Modify", func(t *testing.T) {
			a := LetValue(t, 1)

			t.Run("WhereModifyIsCalled", func(t *testing.T) {
				ModifyLet(t, a, func(t *testing.T, super func() int) int {
					return super() * 2
				})

				t.Run("Child1", func(t *testing.T) {
					RunSetup(t)

					assert.Equal(t, 2, a())
				})

				t.Run("Child2", func(t *testing.T) {
					RunSetup(t)

					assert.Equal(t, 2, a())
				})
			})

			t.Run("Child3", func(t *testing.T) {
				RunSetup(t)

				assert.Equal(t, 1, a())
			})

			t.Run("Child4", func(t *testing.T) {
				ModifyLetValue(t, a, 123)
				RunSetup(t)

				assert.Equal(t, 123, a())
			})
		})

		t.Run("ModifyAfterRunSetupFails", func(t *testing.T) {
			a := LetValue(t, 1)
			RunSetup(t)

			AssertFatalContains(t, func() {
				ModifyLet(t, a, func(t *testing.T, super func() int) int { return 2 })
			}, "ModifyLet must be called before RunSetup")

			AssertFatalContains(t, func() {
				ModifyLetValue(t, a, 2)
			}, "ModifyLetValue must be called before RunSetup")
		})

		t.Run("ModifyInSetupFails", func(t *testing.T) {
			a := LetValue(t, 1)

			AssertFatalContains(t, func() {
				AddSetup(t, func(t *testing.T) {
					ModifyLet(t, a, func(t *testing.T, super func() int) int { return 2 })
				})
				RunSetup(t)
			}, "ModifyLet must be called before RunSetup")
		})

		t.Run("ResetWhenSetupSkipsAfterBuild", func(t *testing.T) {
			callCount := 0
			var a LetWrapper[int]

			t.Run("BuildThenSkip", func(t *testing.T) {
				AddSetup(t, func(t *testing.T) {
					assert.Equal(t, 1, a())
					t.SkipNow()
				})
				a = Let(t, func(t *testing.T) int {
					callCount++
					return callCount
				})
				RunSetup(t)
			})

			t.Run("AfterSkip", func(t *testing.T) {
				RunSetup(t)
				assert.Equal(t, 2, a())
				assert.Equal(t, 2, callCount)
			})
		})

		t.Run("TeardownSeesCachedValueBeforeReset", func(t *testing.T) {
			callCount := 0
			a := Let(t, func(t *testing.T) *Sample {
				callCount++
				return &Sample{value: fmt.Sprintf("v%d", callCount)}
			})
			var closed string

			t.Run("Run", func(t *testing.T) {
				AddSetup(t, func(t *testing.T) {
					// Registered before the Let is first evaluated: the
					// lifecycle reset must run after this teardown.
					t.Cleanup(func() {
						closed = a().value
					})
				})
				RunSetup(t)
				assert.Equal(t, "v1", a().value)
			})

			assert.Equal(t, "v1", closed)
			assert.Equal(t, 1, callCount)
		})

		t.Run("InFlightConstructorTeardownSeesCachedValue", func(t *testing.T) {
			var seen string
			started := make(chan struct{})
			release := make(chan struct{})
			var a LetWrapper[*Sample]
			a = Let(t, func(t *testing.T) *Sample {
				close(started)
				<-release
				t.Cleanup(func() { seen = a().value })
				return &Sample{value: "constructed"}
			})

			t.Run("Run", func(t *testing.T) {
				RunSetup(t)
				go func() { _ = a() }()
				<-started
				// Runs before the RunSetup wait cleanup, unblocking the
				// constructor so it can register its own teardown.
				t.Cleanup(func() { close(release) })
			})

			assert.Equal(t, "constructed", seen)
		})

		t.Run("AfterRunSetupCleanupFails", func(t *testing.T) {
			a := LetValue(t, 1)
			errorMsgs := []string{}
			errorfFuncHook.WithValue(func(format string, args ...any) {
				errorMsgs = append(errorMsgs, fmt.Sprintf(format, args...))
			}).SetupInTest(t)

			t.Run("Run", func(t *testing.T) {
				RunSetup(t)
				assert.Equal(t, 1, a())
			})

			assert.Equal(t, 0, a())
			assert.Len(t, errorMsgs, 1)
			assert.Contains(t, errorMsgs[0], "Must call RunSetup before using Let")
		})

		t.Run("WhenAccessedBeforeRun", func(t *testing.T) {
			a := LetValue(t, 1)

			errorMsgs := []string{}
			errorfFuncHook.WithValue(func(format string, args ...any) {
				errorMsgs = append(errorMsgs, fmt.Sprintf(format, args...))
			}).SetupInTest(t)

			assert.Equal(t, 0, a())
			assert.Len(t, errorMsgs, 1)
			assert.Contains(t, errorMsgs[0], "Must call RunSetup before using Let")
		})

		t.Run("GenerationMismatchPanics", func(t *testing.T) {
			previous := currentCtx.Load()
			currentCtx.Store(&testContext{t: t, generation: 2})
			t.Cleanup(func() { currentCtx.Store(previous) })

			m := &letMetadata[int]{}
			m.state.Store(&letState[int]{generation: 1, status: letStatusBuilt, value: 1})

			assert.Panics(t, func() { m.loadState(2) })
		})

		t.Run("UninitializedStateAllowed", func(t *testing.T) {
			previous := currentCtx.Load()
			currentCtx.Store(&testContext{t: t, generation: 123})
			t.Cleanup(func() { currentCtx.Store(previous) })

			m := &letMetadata[int]{}
			m.state.Store(&letState[int]{status: letStatusInitial})

			assert.NotPanics(t, func() { m.loadState(123) })
		})

		t.Run("BeginConstructionWhenDisallowedReturnsFalse", func(t *testing.T) {
			previous := constructionAllowed.Load()
			constructionAllowed.Store(false)
			t.Cleanup(func() { constructionAllowed.Store(previous) })

			assert.False(t, beginLetConstruction(1, func() {}, func() {}))
		})

		t.Run("BeginConstructionGenerationMismatchPanics", func(t *testing.T) {
			previousCtx := currentCtx.Load()
			previousAllowed := constructionAllowed.Load()
			currentCtx.Store(&testContext{t: t, generation: 2})
			constructionAllowed.Store(true)
			t.Cleanup(func() {
				currentCtx.Store(previousCtx)
				constructionAllowed.Store(previousAllowed)
			})

			assert.Panics(t, func() {
				beginLetConstruction(1, func() {}, func() {})
			})
		})

		t.Run("MetadataWhenPassingUnexpectedValue", func(t *testing.T) {
			AssertFatalContains(t, func() {
				LetWrapper[int](func() int { return 1 }).metadata(t)
			}, "Could not find metadata for Let!")
		})
	})

	t.Run("FatalIfErrorElseReturn", func(t *testing.T) {
		RunSetup(t)
		assert.Equal(t, 1, FatalIfErrorElseReturn(t, nil /* err */, 1))

		AssertFatalContains(t, func() {
			FatalIfErrorElseReturn(t, errors.New("fakeErr"), 1)
		}, "fakeErr")
	})

}
