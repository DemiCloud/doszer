//go:build linux && amd64

package builtin

import "embed"

const platform = "linux_amd64"

//go:embed data/linux_amd64
var embedded embed.FS
