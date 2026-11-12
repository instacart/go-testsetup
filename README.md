# Test Setup (testsetup)

Library for managing setup of tests.

Pre-1.0, maintained on a best-effort basis. See [Project status](#project-status).

## Motivation

Tests often require some shared set up that must be modified slightly between different subtests.

The standard testing package doesn't offer a good way to package setup functions and modify them. This library tries to address those needs by providing some utilities to help manage setup and configuration of tests. See the example usage in the next section.

## Usage

### Setting up shared setup and running on child tests

Import the library (recommend dot import to make things more succint).
```go
import (
    // ...
    . "github.com/instacart/go-testsetup/testsetup"
)
```

Use `AddSetup(func(t *testing.T) { ... })` to add setup functions for the current test or for its children.

Use `RunSetup(t)` to run all the setup functions added with `AddSetup` by the current test and its parents.

Example from [example/example1_test.go](example/example1_test.go):
```go
func TestExample1(t *testing.T) {
	AddSetup(t, func(t *testing.T) {
		fmt.Println("setup1")
	})
	fmt.Println("TestExample1")

	t.Run("Child1", func(t *testing.T) {
		AddSetup(t, func(t *testing.T) {
			fmt.Println("setup2")
		})
		fmt.Println("Child1")

		t.Run("Grandchild1", func(t *testing.T) {
			RunSetup(t)
			fmt.Println("Grandchild1")
		})

		t.Run("Grandchild2", func(t *testing.T) {
			RunSetup(t)
			fmt.Println("Grandchild2")
		})
	})

	t.Run("Child2", func(t *testing.T) {
		RunSetup(t)
		fmt.Println("Child2")
	})
}
```

Output:
```
=== RUN   TestExample1
TestExample1
=== RUN   TestExample1/Child1
Child1
=== RUN   TestExample1/Child1/Grandchild1
setup1
setup2
Grandchild1
--- PASS: TestExample1/Child1/Grandchild1 (0.00s)
=== RUN   TestExample1/Child1/Grandchild2
setup1
setup2
Grandchild2
--- PASS: TestExample1/Child1/Grandchild2 (0.00s)
--- PASS: TestExample1/Child1 (0.00s)
=== RUN   TestExample1/Child2
setup1
Child2
--- PASS: TestExample1/Child2 (0.00s)
--- PASS: TestExample1 (0.00s)
PASS
```

### Modifying a value used in a setup function from a child

Similar to RSpec, you can define lazily computed values using `Let(t, func(t *testing.T) SomeType {...})`.
Those can be modified in child tests.

Call `ModifyLet` (or `ModifyLetValue`) before `RunSetup` for the current test. Once setup
starts, values may already be built, so calling either from a setup function or after
`RunSetup` fails the test instead of leaving a stale cached value.

A `Let` can only be evaluated while `RunSetup` is active. Calls before `RunSetup` or after
its cleanup fail the test. Teardowns registered by setup functions or the test body still
see the cached value; the cache is reset after those teardowns run.

Example from [example/example2_test.go](example/example2_test.go):

```go
package example

import (
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/instacart/go-testsetup/testsetup"
)

type Sample struct{ value string }

func TestExample2(t *testing.T) {
	sample := Let(t, func(t *testing.T) Sample { return Sample{value: "a"} })

	var output string
	AddSetup(t, func(t *testing.T) {
		output = "sample().value = " + sample().value
	})

	t.Run("Child1", func(t *testing.T) {
		ModifyLet(t, sample, func(t *testing.T, super func() Sample) Sample {
			s := super()
			s.value = s.value + "b"
			return s
		})

		t.Run("Grandchild1", func(t *testing.T) {
			RunSetup(t)
			assert.Equal(t, "sample().value = ab", output)
		})
	})

	t.Run("Child2", func(t *testing.T) {
		RunSetup(t)

		assert.Equal(t, "sample().value = a", output)
	})
}
```

## Development

Requires Go 1.25 or later.

Depends on [go-testhooks](https://github.com/instacart/go-testhooks).

```bash
make test
```

## Project status

A variant of this library is used at Instacart. We're publishing it so others
can use, adapt, and build on the ideas.

This is not an officially supported Instacart product and comes with no
commitment to ongoing maintenance. Fixes and improvements are made on a
best-effort basis, and the repository may be archived in the future.

The API is pre-1.0 and may change between minor versions; breaking changes
will be noted in the release notes.

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).
