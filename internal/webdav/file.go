package webdav

import "os"

// hookFile wraps os.File to trigger an upload callback on Close.
type hookFile struct {
	*os.File
	absPath     string
	triggerHook bool
	onClose     OnNoteUpload
}

func (hf *hookFile) Close() error {
	err := hf.File.Close()
	if err == nil && hf.triggerHook && hf.onClose != nil {
		hf.onClose(hf.absPath)
	}
	return err
}
