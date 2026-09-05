//go:build whisper

package wakeword

// Anchor the sherpa-onnx dependency in the whisper (voice) build. Task 4 replaces
// this file's role with the real engine; it exists first to prove the combined
// whisper.cpp(static) + sherpa-onnx(dynamic) build links in-project.
//
// webview_go declares a static-only LDFLAGS directive, which puts GNU ld in
// static mode; sherpa-onnx ships as DLLs (no static import libs), so ld then
// fails to find -lsherpa-onnx-c-api / -lonnxruntime. The cgo directive below
// re-enables dynamic linking so the sherpa DLLs resolve. whisper.cpp's own libs
// are still linked statically by name earlier on the line, so this only affects
// the (dynamic) sherpa/onnxruntime libraries and the trailing system libs.

/*
#cgo windows LDFLAGS: -Wl,-Bdynamic
*/
import "C"

import _ "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
