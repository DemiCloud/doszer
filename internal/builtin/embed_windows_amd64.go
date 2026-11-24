//go:build windows && amd64

package builtin

import "embed"

const platform = "windows_amd64"

//go:embed data/windows_amd64
var embedded embed.FS
