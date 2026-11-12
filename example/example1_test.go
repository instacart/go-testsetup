package example

import (
	"fmt"
	"testing"

	. "github.com/instacart/go-testsetup/testsetup"
)

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
