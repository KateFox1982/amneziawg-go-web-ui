// Package wasm carries the packaged WebAssembly frontend as an embedded file
// system.
//
// The bundle has to be embedded from inside this module: go:embed refuses to
// reach into a different module, so the server - which lives in the parent
// module and only reaches this one through a replace directive - imports this
// package instead of embedding the files itself.
//
// This directory is where "fyne package -os wasm" writes its output, so the
// wrapper lives right in it and nothing has to be copied around: the loader
// page, its light and dark stylesheets, the spinners, wasm_exec.js and the
// bundle itself all land next to this file. Everything here except this file
// is generated - run "make web-ui" - and the "all:" prefix means the embed
// still resolves in a fresh clone, where this source is the only file around.
package wasm

import (
	"embed"
	"io/fs"
)

//go:embed all:*
var files embed.FS

// FS returns the packaged frontend, rooted at the directory this package
// lives in.
func FS() fs.FS { return files }
