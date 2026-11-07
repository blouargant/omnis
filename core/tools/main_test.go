package tools

import (
	"os"
	"testing"

	"github.com/blouargant/omnis/internal/sandbox"
)

// The read-only Bash re-executes the running binary as the sandbox helper;
// in tests that binary is this one.
func TestMain(m *testing.M) {
	sandbox.MaybeRunHelper()
	os.Exit(m.Run())
}
