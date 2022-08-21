package sftp

import (
	"fmt"
	"github.com/Entscheider/sshtool/logger"
	gosftp "github.com/pkg/sftp"
	"io"
	"os"
	"path/filepath"
)

var ErrForbidden = fmt.Errorf("forbidden")

// SimplifiedFS is an interface that list all operation a filesystem should support.
// This filesystem can then be combined, wrapped, and be converted into a [sftp.Handlers] to serve it as sftp filesystem.
type SimplifiedFS interface {
	// List returns a function that should list the entry of the given path.
	// This returned function takes an array of file info along with an offset
	// and fills this array with the files in the directory skipping the first ones (according to the offset).
	// The function then returns the number of [os.FileInfo] actually filled into the array.
	List(path string) (func([]os.FileInfo, int64) (int, error), error)
	// Lstat returns a FileInfo describing the named file. If the file is a symbolic link, the returned FileInfo
	// describes the symbolic link. Lstat makes no attempt to follow the link.
	Lstat(path string) (os.FileInfo, error)
	// Stat returns a FileInfo describing the named file.
	Stat(path string) (os.FileInfo, error)
	// ReadLink returns the destination of the named symbolic link.
	ReadLink(path string) (os.FileInfo, error)

	// Read creates a [io.ReaderAt] object for the file at the given path.
	Read(path string) (io.ReaderAt, error)
	// Write creates a [io.WriterAt] object for the file at the given path.
	Write(path string) (io.WriterAt, error)

	// SetStat sets the attributes according to the given flags for the file at the given path.
	SetStat(path string, flags gosftp.FileAttrFlags, attributes *gosftp.FileStat) error
	// Rename renames the src file into the dst one.
	Rename(src, dst string) error
	// Rmdir removes the directory at the given path.
	Rmdir(path string) error
	// Rm removes the file at the given path.
	Rm(path string) error
	// Mkdir creates a directory at the given path.
	Mkdir(path string) error
	// Link creates a hard link for the src path to the dst path.
	Link(src, dst string) error
	// Symlink creates a symbolic link for the src path to the given dst path.
	Symlink(src, dst string) error
}

// CreateSFTPHandler converts a SimplifiedFS into a [sftp.Handlers] object (to serve this filesystem through sftp)
// while logging relevant access and information using the given logger parameters for the given connection info.
func CreateSFTPHandler(fs SimplifiedFS, accessLogger logger.AccessLogger, info logger.ConnectionInfo, log logger.Logger) gosftp.Handlers {
	w := &wrapper{
		fs, accessLogger, info, log,
	}
	return gosftp.Handlers{
		FileCmd:  w,
		FileGet:  w,
		FilePut:  w,
		FileList: w,
	}
}

// wrapper wraps a SimplifiedFS into a sftp.Handlers for serving it as with sftp.
type wrapper struct {
	fs           SimplifiedFS
	accessLogger logger.AccessLogger
	info         logger.ConnectionInfo
	log          logger.Logger
}

// Logs that access has happened with the given parameter.
func (w *wrapper) logAccess(path, kind, status string) {
	if w.accessLogger == nil {
		return
	}
	w.accessLogger.NewAccess(w.info, path, kind, status)
}

// Logs that an error has happened in the given context with the given error message err.
func (w *wrapper) logError(context string, err error) {
	if w.log == nil {
		return
	}
	w.log.Err("SimplifiedFS", fmt.Sprintf("%s: %s", context, err.Error()))
}

func (w *wrapper) Filecmd(r *gosftp.Request) error {
	path, err := normalizePath(r.Filepath)
	if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during path normalization in Filecmd", err)
		return err
	}
	err = w.filecmdCall(path, r)
	if err == ErrForbidden {
		w.logAccess(path, r.Method, "forbidden")
		return err
	}
	if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during the fileCmd call", err)
		return err
	}
	w.logAccess(path, r.Method, "ok")
	return nil
}

// Handle the different [gosftp.Handlers] FileCmd methods using a valid file path
func (w *wrapper) filecmdCall(path string, r *gosftp.Request) error {
	switch r.Method {
	case "Setstat":
		return w.fs.SetStat(path, r.AttrFlags(), r.Attributes())
	case "Rename":
		target, err := normalizePath(r.Target)
		if err != nil {
			return err
		}
		return w.fs.Rename(path, target)
	case "Rmdir":
		return w.fs.Rmdir(path)
	case "Remove":
		return w.fs.Rm(path)
	case "Mkdir":
		return w.fs.Mkdir(path)
	case "Link":
		target, err := normalizePath(r.Target)
		if err != nil {
			return err
		}
		return w.fs.Link(path, target)
	case "Symlink":
		target, err := normalizePath(r.Target)
		if err != nil {
			return err
		}
		return w.fs.Symlink(path, target)
	}

	return fmt.Errorf("unknow operation %s", r.Method)
}

func (w *wrapper) Fileread(r *gosftp.Request) (io.ReaderAt, error) {
	path, err := normalizePath(r.Filepath)
	if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during the path normlization in file read", err)
		return nil, err
	}
	reader, err := w.fs.Read(path)
	if err == ErrForbidden {
		w.logAccess(path, r.Method, "forbidden")
	} else if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during Fileread", err)
	} else {
		w.logAccess(path, r.Method, "ok")
	}
	return reader, err
}

func (w *wrapper) Filewrite(r *gosftp.Request) (io.WriterAt, error) {
	path, err := normalizePath(r.Filepath)
	if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during the path normalization in file write", err)
		return nil, err
	}
	writer, err := w.fs.Write(path)
	if err == ErrForbidden {
		w.logAccess(path, r.Method, "forbidden")
	} else if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during Filewrite", err)
	} else {
		w.logAccess(path, r.Method, "ok")
	}
	return writer, err
}

func (w *wrapper) Filelist(r *gosftp.Request) (gosftp.ListerAt, error) {
	path, err := normalizePath(r.Filepath)
	if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during path normalization in Filelist", err)
		return nil, err
	}
	res, err := w.fileListCall(path, r)
	if err == ErrForbidden {
		w.logAccess(path, r.Method, "forbidden")
	} else if err != nil {
		w.logAccess(path, r.Method, "error")
		w.logError("Error during Filelist call", err)
	} else {
		w.logAccess(path, r.Method, "ok")
	}
	return res, err
}

// Wraps a function into a ListerAt interface that lists using this function.
type listenerF func([]os.FileInfo, int64) (int, error)

func (l listenerF) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	return l(ls, offset)
}

// Implement ListerAt to only list a single file info.
type singleFileInfo struct {
	os.FileInfo
}

func (l singleFileInfo) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset > 0 {
		return 0, io.EOF
	}
	if len(ls) == 0 {
		return 0, fmt.Errorf("ls is empty")
	}
	ls[0] = l.FileInfo
	return 1, nil
}

// Handle the different sftp.Handlers FileList methods using a valid file path
func (w *wrapper) fileListCall(path string, r *gosftp.Request) (gosftp.ListerAt, error) {
	switch r.Method {
	case "List":
		listF, err := w.fs.List(path)
		if err != nil {
			return nil, err
		}
		return listenerF(listF), nil
	case "Lstat":
		stat, err := w.fs.Lstat(path)
		if err != nil {
			return nil, err
		}
		return singleFileInfo{stat}, nil
	case "Stat":
		stat, err := w.fs.Stat(path)
		if err != nil {
			return nil, err
		}
		return singleFileInfo{stat}, nil
	case "Readlink":
		info, err := w.fs.ReadLink(path)
		if err != nil {
			return nil, err
		}
		return singleFileInfo{info}, nil
	}
	return nil, fmt.Errorf("unknown method %s", r.Method)
}

// normalizePath normalize the given path by replacing windows like \ into unix like / and by
// ensuring that intermediate .. and . are resolved.
// It also ensures that the access is not outside the root directory (e.g. using x/y/../../../z to access a parent
// directory)
func normalizePath(path string) (string, error) {
	path = filepath.Clean(path)
	path = filepath.ToSlash(path)
	if path == "" {
		path = "/"
	}
	if !ContainsValidDir(path) {
		return "", fmt.Errorf("wrong path")
	}
	return path, nil
}

// ContainsValidDir checks that the given path contains no magical characters such as ../ and is otherwise normalized
// (e.g. no // or ./)
func ContainsValidDir(path string) bool {
	n := len(path)
	i := 0
	if path[0] == '/' {
		i = 1
	}
	for i < n {
		if path[i] == '/' && i > 0 {
			// two // without anything between is not allowed
			return false
		}
		if path[i] == '.' && (i+1 == n || path[i+1] == '/') {
			// ./ is not allowed
			return false
		}
		if path[i] == '.' && path[i+1] == '.' && (i+2 == n || path[i+2] == '/') {
			// ../ is absolutely not allowed
			return false
		}
		i = i + 1
		for i < n {
			// fast-forward to next directory /
			if path[i] == '/' {
				i = i + 1
				break
			}
			i = i + 1
		}
	}
	return true
}
