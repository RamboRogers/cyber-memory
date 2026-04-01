//go:build linux && arm64

package assets

import _ "embed"

//go:embed linux_arm64/libonnxruntime.so
var OrtLib []byte

const OrtLibName = "libonnxruntime.so"
