//go:build linux && amd64

package assets

import _ "embed"

//go:embed linux_amd64/libonnxruntime.so
var OrtLib []byte

const OrtLibName = "libonnxruntime.so"
