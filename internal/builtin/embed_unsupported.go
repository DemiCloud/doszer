//go:build !((linux && amd64) || (windows && amd64))

// No DOSBox Pure core is built for this platform: Available reports false.
package builtin

import "embed"

const platform = "unsupported"

//go:embed data/unsupported
var embedded embed.FS
