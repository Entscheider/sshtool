package sftp

import (
	"fmt"
	gosftp "github.com/pkg/sftp"
	"io"
	"os"
	"path/filepath"
	"time"
)

// OsDirFS Parent interface for every os filesystem based implementation
type OsDirFS interface {
	SimplifiedFS
	CanRead(absolutePath string) bool
	CanWrite(absolutePath string) bool
	// IntoAbsPath Converts the given path into an absolute path in the host filesystem
	IntoAbsPath(path string) (string, error)
}

// DirFs implements [sftp.SimplifiedFS] for an operating system native directory.
type DirFs struct {
	// The path of the directory which contents become this filesystem.
	Root string
	// Whether to only support read operations.
	Readonly bool
}

func (d DirFs) statOfRoot() (os.FileInfo, error) {
	stat, err := os.Stat(d.Root)
	if err != nil {
		return nil, err
	}
	return renamedFileInfo{stat, "/"}, nil
}

func (d DirFs) CanRead(_ string) bool {
	return true
}

func (d DirFs) CanWrite(_ string) bool {
	return !d.Readonly
}

func (d DirFs) IntoAbsPath(path string) (string, error) {
	realPath := filepath.Join(d.Root, path)
	realPath = filepath.ToSlash(realPath)
	if len(realPath) == len(d.Root)-1 {
		realPath += "/"
	}
	if !ContainsValidDir(realPath) || realPath[:len(d.Root)] != d.Root {
		return "", fmt.Errorf("invalid path")
	}
	return realPath, nil
}

func (d DirFs) List(path string) (func([]os.FileInfo, int64) (int, error), error) {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return nil, err
	}
	if !d.CanRead(abspath) {
		return nil, ErrForbidden
	}
	// We cache all infos before returning the actual function.
	dirinfos, err := os.ReadDir(abspath)
	if err != nil {
		return nil, err
	}
	fileinfos := make([]os.FileInfo, len(dirinfos))
	for i, dirinfo := range dirinfos {
		fileinfos[i], err = dirinfo.Info()
		if err != nil {
			return nil, err
		}
	}
	return func(ls []os.FileInfo, offset int64) (int, error) {
		if offset >= int64(len(fileinfos)) {
			return 0, io.EOF
		}
		n := copy(ls, fileinfos[offset:])
		if n < len(ls) {
			return n, io.EOF
		}
		return n, nil
	}, nil
}

func (d DirFs) Stat(path string) (os.FileInfo, error) {
	if path == "/" {
		return d.statOfRoot()
	}
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return nil, err
	}
	if !d.CanRead(abspath) {
		return nil, ErrForbidden
	}
	return os.Stat(abspath)
}

func (d DirFs) Lstat(path string) (os.FileInfo, error) {
	if path == "/" {
		return d.statOfRoot()
	}
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return nil, err
	}
	if !d.CanRead(abspath) {
		return nil, ErrForbidden
	}
	return os.Lstat(abspath)
}

func (d DirFs) ReadLink(path string) (os.FileInfo, error) {
	if path == "/" {
		return d.statOfRoot()
	}
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return nil, err
	}
	if !d.CanRead(abspath) {
		return nil, ErrForbidden
	}
	res, err := os.Readlink(abspath)
	if err != nil {
		return nil, err
	}
	if !d.CanRead(res) {
		return nil, ErrForbidden
	}
	return os.Stat(res)
}

func (d DirFs) Read(path string) (io.ReaderAt, error) {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return nil, err
	}
	if !d.CanRead(abspath) {
		return nil, ErrForbidden
	}
	return os.Open(abspath)
}

func (d DirFs) Write(path string) (io.WriterAt, error) {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return nil, err
	}
	if !d.CanWrite(abspath) {
		return nil, ErrForbidden
	}
	return os.OpenFile(abspath, os.O_WRONLY|os.O_CREATE, 0o644)
}

func (d DirFs) SetStat(path string, flags gosftp.FileAttrFlags, attributes *gosftp.FileStat) error {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return err
	}
	if !d.CanWrite(abspath) {
		return ErrForbidden
	}

	// flags tells us which attributes actual has to be overwritten.
	if flags.Size { // overwrite the size
		err := os.Truncate(abspath, int64(attributes.Size))
		if err != nil {
			return err
		}
	}
	if flags.Permissions { // overwrite the permission
		err := os.Chmod(abspath, attributes.FileMode())
		if err != nil {
			return err
		}
	}
	if flags.UidGid { // overwrite user id and group id
		err := os.Chown(abspath, int(attributes.UID), int(attributes.GID))
		if err != nil {
			return err
		}
	}
	if flags.Acmodtime { // overwrite the access timestamp (atime) and modification timestamp (mtime)
		atime := time.Unix(int64(attributes.Atime), 0)
		mtime := time.Unix(int64(attributes.Mtime), 0)
		err := os.Chtimes(abspath, atime, mtime)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d DirFs) Rename(src, dst string) error {
	absSrc, err := d.IntoAbsPath(src)
	if err != nil {
		return err
	}
	absDst, err := d.IntoAbsPath(dst)
	if err != nil {
		return err
	}
	if !d.CanWrite(absSrc) || !d.CanWrite(absDst) {
		return ErrForbidden
	}
	return os.Rename(absSrc, absDst)
}

func (d DirFs) Rmdir(path string) error {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return err
	}
	if !d.CanWrite(abspath) {
		return ErrForbidden
	}
	stat, err := os.Stat(abspath)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("not a directory %s", path)
	}
	return os.Remove(abspath)
}

func (d DirFs) Rm(path string) error {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return err
	}
	if !d.CanWrite(abspath) {
		return ErrForbidden
	}
	stat, err := os.Stat(abspath)
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return fmt.Errorf("is a directory %s", path)
	}
	return os.Remove(abspath)
}

func (d DirFs) Mkdir(path string) error {
	abspath, err := d.IntoAbsPath(path)
	if err != nil {
		return err
	}
	if !d.CanWrite(abspath) {
		return ErrForbidden
	}
	return os.Mkdir(abspath, 0o755)
}

func (d DirFs) Link(src, dst string) error {
	absSrc, err := d.IntoAbsPath(src)
	if err != nil {
		return err
	}
	absDst, err := d.IntoAbsPath(dst)
	if err != nil {
		return err
	}
	if !d.CanRead(absSrc) || !d.CanWrite(absDst) {
		return ErrForbidden
	}
	return os.Link(absSrc, absDst)
}

func (d DirFs) Symlink(src, dst string) error {
	absSrc, err := d.IntoAbsPath(src)
	if err != nil {
		return err
	}
	absDst, err := d.IntoAbsPath(dst)
	if err != nil {
		return err
	}
	if !d.CanRead(absSrc) || !d.CanWrite(absDst) {
		return ErrForbidden
	}
	return os.Symlink(absSrc, absDst)
}
