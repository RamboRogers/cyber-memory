//go:build !(darwin && arm64) && !(darwin && amd64) && !(linux && amd64) && !(linux && arm64) && !(windows && amd64)

package assets

// OrtLib is nil on platforms without an embedded ORT library.
// The engine will fall back to CYBER_MEMORY_ORT or system default.
var OrtLib []byte

const OrtLibName = ""
