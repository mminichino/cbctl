//go:build windows

package output

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

var vtState sync.Map // windows.Handle -> bool, so SetConsoleMode runs once per console handle

// platformColorOK enables virtual terminal processing on a console handle so that ANSI escape
// codes render. A writer that is not a console (a pipe or file written with --color=always) needs
// nothing. If the console refuses, color stays off and no escapes are written.
func platformColorOK(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return true
	}
	h := windows.Handle(f.Fd())
	if v, ok := vtState.Load(h); ok {
		return v.(bool)
	}
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		vtState.Store(h, true) // not a console
		return true
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
		if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
			vtState.Store(h, false)
			return false
		}
	}
	vtState.Store(h, true)
	return true
}
