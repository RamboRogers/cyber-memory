//go:build windows && amd64

package assets

import _ "embed"

//go:embed windows_amd64/onnxruntime.dll
var OrtLib []byte

const OrtLibName = "onnxruntime.dll"
