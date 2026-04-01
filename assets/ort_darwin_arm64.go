//go:build darwin && arm64

package assets

import _ "embed"

//go:embed darwin_arm64/libonnxruntime.dylib
var OrtLib []byte

const OrtLibName = "libonnxruntime.dylib"
