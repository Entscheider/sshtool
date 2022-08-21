package sftp

import (
	"errors"
	"fmt"
	"github.com/Entscheider/sshtool/logger"
	gosftp "github.com/pkg/sftp"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CombinedFS combines different SimplifiedFS by serving it as a subdirectory to the root of this filesystem.
// It is not recommended nesting several CombinedFS.
type CombinedFS struct {
	Dirs    map[string]SimplifiedFS
	logging logger.Logger
}

// Extract gets the filesystem that handles the given path and returns subpath within this filesystem and the
// filesystem itself.
func (c CombinedFS) Extract(path string) (string, SimplifiedFS, error) {
	// Remove a starting slash.
	if path[0] == '/' {
		path = path[1:]
	}
	// Iterate through every subdirectory
	for name, sfs := range c.Dirs {
		// If the path belongs to this filesystem, we return it
		if len(path) >= len(name) && path[:len(name)] == name {
			// path = name + '/....' or path = name ?
			subpath := path[len(name):]
			if len(subpath) == 0 {
				subpath = "/"
			}
			// Only return it if we are actually in this sub filesystem.
			if subpath[0] == '/' {
				return subpath, sfs, nil
			}
		}
	}
	return "", nil, os.ErrNotExist
}

func min(a, b int64) int64 {
	if a < b {
		return a
	} else {
		return b
	}
}

// FileInfo of a virtual directory with the given name
type topDirPath string

func (t topDirPath) Name() string {
	return string(t)
}

func (t topDirPath) Size() int64 {
	return 480
}

func (t topDirPath) Mode() fs.FileMode {
	// ! important ! to return also os.ModeDir here
	if t.IsDir() {
		return os.FileMode(0755) | os.ModeDir
	}
	return os.FileMode(0644)
}

func (t topDirPath) ModTime() time.Time {
	return time.Date(2022, time.March, 20, 0, 0, 0, 0, time.UTC)
}

func (t topDirPath) IsDir() bool {
	return true
}

func (t topDirPath) Sys() interface{} {
	return t
}

func (c CombinedFS) List(path string) (func([]os.FileInfo, int64) (int, error), error) {
	// If we are at root, we list all sub filesystems.
	if path == "/" {
		// Get every key (=name) from filesystem map.
		topDirs := make([]string, len(c.Dirs))
		i := 0
		for name := range c.Dirs {
			topDirs[i] = name
			i += 1
		}
		sort.Strings(topDirs)

		// Return a function that copies the filesystem info into the desired FileInfo array.
		return func(fs []os.FileInfo, offset int64) (int, error) {
			if offset >= int64(len(topDirs)) {
				return 0, io.EOF
			}
			// remaining is the number of FileInfo objects we copy into the fs array
			remaining := min(int64(len(topDirs))-offset, int64(len(fs))-offset)
			for i := 0; i < int(remaining); i++ {
				// Get the name, create a FileInfo object for it and add into the fs array
				dirname := topDirs[int(offset)+i]
				//fs[i] = topDirPath(dirname)
				stat, err := c.Dirs[dirname].Stat("/")
				if err != nil {
					c.logging.Err("CombineFS List", err.Error())
					return 0, err
				}
				// we cannot use the root FileInfo directly, we first have to name it accordingly to the
				// sub filesystem directory name.
				fs[i] = renamedFileInfo{stat, dirname}
			}
			if int(offset+remaining) == len(topDirs) {
				return int(remaining), io.EOF
			}
			return int(remaining), nil
		}, nil
	}
	// Otherwise we can use the List method of the sub filesystem.
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return nil, err
	}
	return sfs.List(subpath)
}

// Get the FileInfo of the root directory of this combined fs
func (c CombinedFS) statOfRoot() os.FileInfo {
	return topDirPath('/')
}

// renamedFileInfo modifies the name of a given FileInfo
type renamedFileInfo struct {
	original os.FileInfo
	newName  string
}

func (k renamedFileInfo) Name() string {
	return k.newName
}

func (k renamedFileInfo) Size() int64 {
	return k.original.Size()
}

func (k renamedFileInfo) Mode() fs.FileMode {
	return k.original.Mode()
}

func (k renamedFileInfo) ModTime() time.Time {
	return k.original.ModTime()
}

func (k renamedFileInfo) IsDir() bool {
	return k.original.IsDir()
}

func (k renamedFileInfo) Sys() interface{} {
	return k.original.Sys()
}

func (c CombinedFS) Stat(path string) (os.FileInfo, error) {
	// For root directory, we use a generated one
	if path == "/" {
		return c.statOfRoot(), nil
	}
	// For the sub filesystem we use the state of it.
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return nil, err
	}
	stat, err := sfs.Stat(subpath)
	if err != nil {
		return nil, err
	}
	// we need to rename the root entry of the sub filesystem because the c.Dirs entry possible has a
	// different name than the directory has in the combined fs.
	if subpath == "/" {
		return renamedFileInfo{stat, filepath.Base(path)}, nil
	}
	return stat, nil
}

func (c CombinedFS) Lstat(path string) (os.FileInfo, error) {
	// Same to Stat method
	if path == "/" {
		return c.statOfRoot(), nil
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return nil, err
	}
	stat, err := sfs.Lstat(subpath)
	if err != nil {
		return nil, err
	}
	if subpath == "/" {
		return renamedFileInfo{stat, filepath.Base(path)}, nil
	}
	return stat, nil
}

func (c CombinedFS) ReadLink(path string) (os.FileInfo, error) {
	if path == "/" {
		return c.statOfRoot(), nil
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return nil, err
	}
	return sfs.ReadLink(subpath)
}

type errReader struct{ err error }

func (e errReader) ReadAt(_ []byte, _ int64) (n int, err error) {
	return 0, e.err
}

func (c CombinedFS) Read(path string) (io.ReaderAt, error) {
	// We delegate the method to the relevant sub filesystem
	if path == "/" {
		return errReader{errors.New("is a directory")}, nil
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return nil, err
	}
	return sfs.Read(subpath)
}

func (c CombinedFS) Write(path string) (io.WriterAt, error) {
	// We delegate the method to the relevant sub filesystem
	if path == "/" {
		return nil, os.ErrInvalid
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return nil, err
	}
	return sfs.Write(subpath)
}

func (c CombinedFS) SetStat(path string, flags gosftp.FileAttrFlags, attributes *gosftp.FileStat) error {
	// We delegate the method to the relevant sub filesystem
	if path == "/" {
		return os.ErrPermission
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return err
	}
	return sfs.SetStat(subpath, flags, attributes)
}

// Converts an [io.WriterAt] into an [io.Writer]
type writerWrapper struct {
	io.WriterAt
	offset int64
}

func (w *writerWrapper) Write(p []byte) (n int, err error) {
	read, err := w.WriteAt(p, w.offset)
	if err != nil {
		return read, err
	}
	w.offset += int64(read)
	return read, err
}

// Converts an [io.ReaderAt] into an [io.Reader]
type readWrapper struct {
	io.ReaderAt
	offset int64
}

func (w *readWrapper) Read(p []byte) (n int, err error) {
	read, err := w.ReadAt(p, w.offset)
	if err != nil {
		return read, err
	}
	w.offset += int64(read)
	return read, err
}

// Fallback function that renames a file by copying it from one into another filesystems
// and remove the source file.
func renameFileFallback(srcSfs, dstSfs SimplifiedFS, srcPath, dstPath string) error {
	reader, err := srcSfs.Read(srcPath)
	if err != nil {
		return err
	}
	writer, err := dstSfs.Write(dstPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(&writerWrapper{writer, 0}, &readWrapper{reader, 0})
	if err != nil {
		return err
	}
	return srcSfs.Rm(srcPath)
}

// Fallback function that renames a directory by copying its content from one filesystem into the other
// and removing the source directory after that.
func renameDirectoryFallback(srcSfs, dstSfs SimplifiedFS, srcPath, dstPath string) error {
	// Create the new directory
	err := dstSfs.Mkdir(dstPath)
	if err != nil {
		return err
	}
	// recursively iterate over all entries
	subdirIter, err := srcSfs.List(srcPath)
	if err != nil {
		return err
	}
	buffer := make([]os.FileInfo, 5)
	offset := int64(0)
	for true {
		read, err := subdirIter(buffer, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if read == 0 {
			break
		}
		offset += int64(read)
		for _, element := range buffer[:read] {
			recSrcPath := filepath.Join(srcPath, element.Name())
			recDstPath := filepath.Join(dstPath, element.Name())
			if element.IsDir() {
				err = renameDirectoryFallback(srcSfs, dstSfs, recSrcPath, recDstPath)
			} else {
				err = renameFileFallback(srcSfs, dstSfs, recSrcPath, recDstPath)
			}
			if err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	// delete directory when finished
	return srcSfs.Rmdir(srcPath)
}

func (c CombinedFS) Rename(src, dst string) error {
	if src == "/" || dst == "/" {
		return os.ErrPermission
	}
	// Cannot rename into itself
	if src == dst {
		return nil
	}

	subSrc, srcSfs, err := c.Extract(src)
	if err != nil {
		return err
	}
	subDst, dstSfs, err := c.Extract(dst)
	if err != nil {
		return err
	}

	// Cannot rename from one root directory to another
	if subSrc == "/" || subDst == "/" {
		return os.ErrInvalid
	}

	// same fs -> can rename it directly
	if srcSfs == dstSfs {
		return srcSfs.Rename(subSrc, subDst)
	}
	// both are os dir fs -> we can also rename it directly
	// Note that this is an ugly optimization and should probably be done by using an appropriate interface.
	if srcDirFs, ok := srcSfs.(OsDirFS); ok {
		if dstDirFs, ok := dstSfs.(OsDirFS); ok {
			absSrc, err := srcDirFs.IntoAbsPath(subSrc)
			if err != nil {
				return err
			}
			absDst, err := dstDirFs.IntoAbsPath(subDst)
			if err != nil {
				return err
			}
			if !srcDirFs.CanWrite(absSrc) || !dstDirFs.CanWrite(absDst) {
				return ErrForbidden
			}
			return os.Rename(absSrc, absDst)
		}
	}
	// fallback, we need to copy and remove
	srcStat, err := srcSfs.Stat(subSrc)
	if err != nil {
		return err
	}
	_, err = dstSfs.Stat(subDst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err == nil { // file does exist
		// TODO: Recursively remove the target path instead of returning this error
		return fmt.Errorf("target path exists")
	}
	if srcStat.IsDir() {
		return renameDirectoryFallback(srcSfs, dstSfs, subSrc, subDst)
	} else {
		return renameFileFallback(srcSfs, dstSfs, subSrc, subDst)
	}
}

func (c CombinedFS) Rmdir(path string) error {
	if path == "/" {
		return os.ErrPermission
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return err
	}
	if subpath == "/" {
		return os.ErrPermission
	}
	return sfs.Rmdir(subpath)
}

func (c CombinedFS) Rm(path string) error {
	if path == "/" {
		return os.ErrPermission
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return err
	}
	if subpath == "/" {
		return os.ErrPermission
	}
	return sfs.Rm(subpath)
}

func (c CombinedFS) Mkdir(path string) error {
	if path == "/" {
		return os.ErrPermission
	}
	subpath, sfs, err := c.Extract(path)
	if err != nil {
		return err
	}
	if subpath == "/" {
		return os.ErrPermission
	}
	return sfs.Mkdir(subpath)
}

func (c CombinedFS) Link(src, dst string) error {
	if src == "/" || dst == "/" {
		return os.ErrPermission
	}
	subSrc, srcSfs, err := c.Extract(src)
	if err != nil {
		return err
	}
	subDst, dstSfs, err := c.Extract(dst)
	if err != nil {
		return err
	}
	if subSrc == "/" || subDst == "/" {
		return os.ErrPermission
	}
	// same fs -> can rename it directly
	if srcSfs == dstSfs {
		return srcSfs.Link(subSrc, subDst)
	}
	// both are os dir fs -> we can also rename it directly
	// Note that this is an ugly optimization and should probably be done by using an appropriate interface.
	if srcDirFs, ok := srcSfs.(OsDirFS); ok {
		if dstDirFs, ok := dstSfs.(OsDirFS); ok {
			absSrc, err := srcDirFs.IntoAbsPath(subSrc)
			if err != nil {
				return err
			}
			absDst, err := srcDirFs.IntoAbsPath(subDst)
			if err != nil {
				return err
			}
			if !srcDirFs.CanWrite(absSrc) || !dstDirFs.CanWrite(absDst) {
				return ErrForbidden
			}
			return os.Link(absSrc, absDst)
		}
	}
	// otherwise, we cannot link
	return fmt.Errorf("cannot link between different file systems")
}

func (c CombinedFS) Symlink(src, dst string) error {
	if src == "/" || dst == "/" {
		return os.ErrPermission
	}
	subSrc, srcSfs, err := c.Extract(src)
	if err != nil {
		return err
	}
	subDst, dstSfs, err := c.Extract(dst)
	if err != nil {
		return err
	}
	if subSrc == "/" || subDst == "/" {
		return os.ErrPermission
	}
	// same fs -> can rename it directly
	if srcSfs == dstSfs {
		return srcSfs.Symlink(subSrc, subDst)
	}
	// both are os dir fs -> we can also rename it directly
	// Note that this is an ugly optimization and should probably be done by using an appropriate interface.
	if srcDirFs, ok := srcSfs.(OsDirFS); ok {
		if dstDirFs, ok := dstSfs.(OsDirFS); ok {
			absSrc, err := srcDirFs.IntoAbsPath(subSrc)
			if err != nil {
				return err
			}
			absDst, err := srcDirFs.IntoAbsPath(subDst)
			if err != nil {
				return err
			}
			if !srcDirFs.CanWrite(absSrc) || !dstDirFs.CanWrite(absDst) {
				return ErrForbidden
			}
			return os.Symlink(absSrc, absDst)
		}
	}
	// otherwise, we cannot link
	return fmt.Errorf("cannot symlink between different file systems")
}
