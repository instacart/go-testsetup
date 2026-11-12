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
