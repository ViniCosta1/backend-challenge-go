package bootstrap

import (
	"testing"

	"go.uber.org/fx"
)

func TestModuleGraphIsComplete(t *testing.T) {
	if err := fx.ValidateApp(Module); err != nil {
		t.Fatal(err)
	}
}
