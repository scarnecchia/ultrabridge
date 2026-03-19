package notestore

import (
	"strings"
	"time"
)

// FileType classifies a file in the notes tree.
type FileType string

const (
	FileTypeNote  FileType = "note"
	FileTypePDF   FileType = "pdf"
	FileTypeEPUB  FileType = "epub"
	FileTypeOther FileType = "other"
)

// ClassifyFileType maps a file extension to a FileType.
// ext must include the leading dot (e.g. ".note").
func ClassifyFileType(ext string) FileType {
	switch strings.ToLower(ext) {
	case ".note":
		return FileTypeNote
	case ".pdf":
		return FileTypePDF
	case ".epub":
		return FileTypeEPUB
	default:
		return FileTypeOther
	}
}

// NoteFile is the view model returned by NoteStore methods.
// JobStatus is the latest processing status from the jobs table,
// empty string if no job exists.
type NoteFile struct {
	Path      string
	RelPath   string
	Name      string
	IsDir     bool
	FileType  FileType
	SizeBytes int64
	MTime     time.Time
	JobStatus string
}
