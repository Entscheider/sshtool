package sftp

import (
	gosftp "github.com/pkg/sftp"
	"io"
	"os"
)

// EmptyFS is a [sftp.SimplifiedFS] that has no content at all.
type EmptyFS struct{}

func (e EmptyFS) List(_ string) (func([]os.FileInfo, int64) (int, error), error) {
	return func(infos []os.FileInfo, i int64) (int, error) {
		return 0, io.EOF
	}, nil
}

func (e EmptyFS) rootFileInfo() os.FileInfo {
	return topDirPath("/")
}

func (e EmptyFS) Lstat(path string) (os.FileInfo, error) {
	if path == "/" {
		return e.rootFileInfo(), nil
	}
	return nil, os.ErrInvalid
}

func (e EmptyFS) Stat(path string) (os.FileInfo, error) {
	if path == "/" {
		return e.rootFileInfo(), nil
	}
	return nil, os.ErrInvalid
}

func (e EmptyFS) ReadLink(path string) (os.FileInfo, error) {
	if path == "/" {
		return e.rootFileInfo(), nil
	}
	return nil, os.ErrInvalid
}

func (e EmptyFS) Read(_ string) (io.ReaderAt, error) {
	return nil, os.ErrNotExist
}

func (e EmptyFS) Write(_ string) (io.WriterAt, error) {
	return nil, os.ErrPermission
}

func (e EmptyFS) SetStat(_ string, _ gosftp.FileAttrFlags, _ *gosftp.FileStat) error {
	return os.ErrPermission
}

func (e EmptyFS) Rename(_, _ string) error {
	return os.ErrPermission
}

func (e EmptyFS) Rmdir(_ string) error {
	return os.ErrPermission
}

func (e EmptyFS) Rm(_ string) error {
	return os.ErrPermission
}

func (e EmptyFS) Mkdir(_ string) error {
	return os.ErrPermission
}

func (e EmptyFS) Link(_, _ string) error {
	return os.ErrPermission
}

func (e EmptyFS) Symlink(_, _ string) error {
	return os.ErrPermission
}
