//go:build darwin && amd64

package assets

import _ "embed"

//go:embed darwin_amd64/libonnxruntime.dylib
var OrtLib []byte

const OrtLibName = "libonnxruntime.dylib"
