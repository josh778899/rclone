// Test Local filesystem interface
package local_test

import (
	"testing"

	"github.com/clive2000/rclone/backend/local"
	"github.com/clive2000/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "",
		NilObject:  (*local.Object)(nil),
	})
}
