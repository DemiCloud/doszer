//go:build linux && arm64

package builtin

import "embed"

const platform = "linux_arm64"

//go:embed data/linux_arm64
var embedded embed.FS
