//go:build all

package tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/Massad/gin-boilerplate/internal/platform/validate"
	"github.com/gin-gonic/gin/binding"
)

// TestMain pins the disposable environment for the whole test binary: the
// Redis index 2, the artisan_test database and the binding validator (the
// entrypoint pin no longer reaches this binary since app.NewRouter does not
// mutate global state). The clients must be created while both environment
// pins are present (godotenv never overrides an already-set variable), so no
// run can drift back to the dev database.
func TestMain(m *testing.M) {
	if err := os.Setenv("REDIS_DB", "2"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := os.Setenv("DB_NAME", "artisan_test"); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	binding.Validator = new(validate.DefaultValidator)
	os.Exit(m.Run())
}
