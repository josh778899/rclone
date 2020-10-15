// Test Cache filesystem interface

// +build !plan9,!js
// +build !race

package cache_test

import (
	"testing"

	"github.com/clive2000/rclone/backend/cache"
	_ "github.com/clive2000/rclone/backend/local"
	"github.com/clive2000/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName:                   "TestCache:",
		NilObject:                    (*cache.Object)(nil),
		UnimplementableFsMethods:     []string{"PublicLink", "OpenWriterAt"},
		UnimplementableObjectMethods: []string{"MimeType", "ID", "GetTier", "SetTier"},
		SkipInvalidUTF8:              true, // invalid UTF-8 confuses the cache
	})
}
