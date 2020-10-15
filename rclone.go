// Sync files and directories to and from local and remote object stores
//
// Nick Craig-Wood <nick@craig-wood.com>
package main

import (
	_ "github.com/clive2000/rclone/backend/all" // import all backends
	"github.com/clive2000/rclone/cmd"
	_ "github.com/clive2000/rclone/cmd/all"    // import all commands
	_ "github.com/clive2000/rclone/lib/plugin" // import plugins
)

func main() {
	cmd.Main()
}
