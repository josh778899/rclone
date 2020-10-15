// +build go1.13,!plan9

// Test Tardigrade filesystem interface
package tardigrade_test

import (
	"testing"

	"github.com/clive2000/rclone/backend/tardigrade"
	"github.com/clive2000/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestTardigrade:",
		NilObject:  (*tardigrade.Object)(nil),
	})
}
