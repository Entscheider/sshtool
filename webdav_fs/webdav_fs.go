package webdav_fs

import (
	"context"
	"github.com/Entscheider/sshtool/logger"
	"github.com/Entscheider/sshtool/sftp"
	"golang.org/x/net/webdav"
	"io"
	"io/fs"
	"net/http"
	"os"
)

// CreateHandlerForFS converts a [sftp.SimplifiedFS] into a [webdav.Handler]
func CreateHandlerForFS(fs sftp.SimplifiedFS, logger logger.Logger) *webdav.Handler {
	return &webdav.Handler{
		LockSystem: webdav.NewMemLS(),
		FileSystem: fileSystemWrapper{fs},
		Logger: func(r *http.Request, err error) {
			if err != nil {
				logger.Err("webdav", err.Error())
			}
		},
	}
}

type fileSystemWrapper struct {
	inner sftp.SimplifiedFS
}

func (f fileSystemWrapper) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	return f.inner.Mkdir(name)
}

func (f fileSystemWrapper) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if (flag&os.O_CREATE == os.O_CREATE || flag&os.O_WRONLY == os.O_WRONLY) && flag&os.O_RDONLY == os.O_RDONLY || flag&os.O_RDWR == os.O_RDWR {
		return &webdavReadWriteFile{
			f.inner,
			name,
			nil,
			nil,
			0,
			nil,
		}, nil
	}
	if flag&os.O_CREATE == os.O_CREATE || flag&os.O_WRONLY == os.O_WRONLY {
		return &webdavWriteFile{
			f.inner,
			name,
			nil,
			nil,
			0,
		}, nil
	}

	reader, err := f.inner.Read(name)
	if err != nil {
		return nil, err
	}
	return &webdavReadFile{
		f.inner,
		name,
		reader,
		0,
		nil,
	}, nil
}

func (f fileSystemWrapper) RemoveAll(_ context.Context, name string) error {
	return f.inner.Rm(name)
}

func (f fileSystemWrapper) Rename(_ context.Context, oldName, newName string) error {
	return f.inner.Rename(oldName, newName)
}

func (f fileSystemWrapper) Stat(_ context.Context, name string) (os.FileInfo, error) {
	return f.inner.Stat(name)
}

type webdavReadFile struct {
	fs       sftp.SimplifiedFS
	filename string
	inner    io.ReaderAt
	offset   int64
	stat     os.FileInfo
}

func (w *webdavReadFile) file() (io.ReaderAt, error) {
	if w.inner != nil {
		return w.inner, nil
	}
	reader, err := w.fs.Read(w.filename)
	if err == nil {
		w.inner = reader
	}
	return reader, nil
}

func (w *webdavReadFile) Close() error {
	if w.inner == nil {
		return nil
	}
	closer, ok := w.inner.(io.Closer)
	if ok {
		return closer.Close()
	}
	return nil
}

func (w *webdavReadFile) Read(p []byte) (int, error) {
	file, err := w.file()
	if err != nil {
		return 0, err
	}
	n, err := file.ReadAt(p, w.offset)
	if err == nil {
		w.offset += int64(n)
	}
	return n, err
}

func (w *webdavReadFile) Seek(offset int64, whence int) (int64, error) {
	absOffset := int64(-1)
	stat, err := w.Stat()
	if err != nil {
		return 0, err
	}
	if whence == io.SeekStart {
		absOffset = offset
	} else if whence == io.SeekCurrent {
		absOffset = w.offset + offset
	} else if whence == io.SeekEnd {
		absOffset = stat.Size() - 1 - offset
	}
	if absOffset < 0 || absOffset >= stat.Size() {
		return absOffset, os.ErrInvalid
	}
	w.offset = absOffset
	return absOffset, nil
}

func (w *webdavReadFile) Readdir(count int) ([]fs.FileInfo, error) {
	if count < 0 {
		return []fs.FileInfo{}, os.ErrInvalid
	}
	lsf, err := w.fs.List(w.filename)
	if err != nil {
		return []fs.FileInfo{}, err
	}
	if count == 0 {
		var total []fs.FileInfo
		i := int64(0)
		for {
			result := make([]fs.FileInfo, 10)
			n, err := lsf(result, i)
			if err != nil && err != io.EOF {
				return []fs.FileInfo{}, err
			}
			i += int64(n)
			total = append(total, result[:n]...)
			if err == io.EOF {
				break
			}
		}
		return total, nil
	}
	result := make([]fs.FileInfo, count)
	n, err := lsf(result, 0)
	return result[:n], err
}

func (w *webdavReadFile) Stat() (fs.FileInfo, error) {
	if w.stat == nil {
		stat, err := w.fs.Stat(w.filename)
		if err != nil {
			return nil, err
		}
		w.stat = stat
	}
	return w.stat, nil
}

func (w *webdavReadFile) Write(_ []byte) (n int, err error) {
	return 0, os.ErrPermission
}

type webdavWriteFile struct {
	fs       sftp.SimplifiedFS
	filename string
	inner    io.WriterAt
	stat     os.FileInfo
	offset   int64
}

func (w *webdavWriteFile) file() (io.WriterAt, error) {
	if w.inner != nil {
		return w.inner, nil
	}
	writer, err := w.fs.Write(w.filename)
	if err == nil {
		w.inner = writer
	}
	return writer, nil
}

func (w *webdavWriteFile) Close() error {
	if w.inner == nil {
		return nil
	}
	closer, ok := w.inner.(io.Closer)
	if ok {
		return closer.Close()
	}
	return nil
}

func (w *webdavWriteFile) Read(_ []byte) (n int, err error) {
	return 0, os.ErrPermission
}

func (w *webdavWriteFile) Seek(offset int64, whence int) (int64, error) {
	absOffset := int64(-1)
	stat, err := w.Stat()
	if err != nil {
		return 0, err
	}
	if whence == io.SeekStart {
		absOffset = offset
	} else if whence == io.SeekCurrent {
		absOffset = w.offset + offset
	} else if whence == io.SeekEnd {
		absOffset = stat.Size() - 1 - offset
	}
	if absOffset < 0 || absOffset >= stat.Size() {
		return absOffset, os.ErrInvalid
	}
	w.offset = absOffset
	return absOffset, nil

}

func (w *webdavWriteFile) Readdir(count int) ([]fs.FileInfo, error) {
	return []fs.FileInfo{}, os.ErrPermission
}

func (w *webdavWriteFile) Stat() (fs.FileInfo, error) {
	if w.stat == nil {
		stat, err := w.fs.Stat(w.filename)
		if err != nil {
			return nil, err
		}
		w.stat = stat
	}
	return w.stat, nil
}

func (w *webdavWriteFile) Write(p []byte) (int, error) {
	file, err := w.file()
	if err != nil {
		return 0, err
	}
	n, err := file.WriteAt(p, w.offset)
	if err == nil {
		w.offset += int64(n)
	}
	return n, err
}

type webdavReadWriteFile struct {
	fs       sftp.SimplifiedFS
	filename string
	reader   io.ReaderAt
	writer   io.WriterAt
	offset   int64
	stat     os.FileInfo
}

func (w *webdavReadWriteFile) Read(p []byte) (int, error) {
	reader, err := w.fileReader()
	if err != nil {
		return 0, err
	}
	n, err := reader.ReadAt(p, w.offset)
	if err != nil {
		return n, err
	}
	w.offset += int64(n)
	return n, nil
}

func (w *webdavReadWriteFile) Seek(offset int64, whence int) (int64, error) {
	absOffset := int64(-1)
	stat, err := w.Stat()
	if err != nil {
		return 0, err
	}
	if whence == io.SeekStart {
		absOffset = offset
	} else if whence == io.SeekCurrent {
		absOffset = w.offset + offset
	} else if whence == io.SeekEnd {
		absOffset = stat.Size() - 1 - offset
	}
	if absOffset < 0 || absOffset >= stat.Size() {
		return absOffset, os.ErrInvalid
	}
	w.offset = absOffset
	return absOffset, nil
}

func (w *webdavReadWriteFile) Readdir(count int) ([]fs.FileInfo, error) {
	if count < 0 {
		return []fs.FileInfo{}, os.ErrInvalid
	}
	lsf, err := w.fs.List(w.filename)
	if err != nil {
		return []fs.FileInfo{}, err
	}
	if count == 0 {
		var total []fs.FileInfo
		i := int64(0)
		for {
			result := make([]fs.FileInfo, 10)
			n, err := lsf(result, i)
			if err != nil && err != io.EOF {
				return []fs.FileInfo{}, err
			}
			i += int64(n)
			total = append(total, result[:n]...)
			if err == io.EOF {
				break
			}
		}
		return total, nil
	}
	result := make([]fs.FileInfo, count)
	n, err := lsf(result, 0)
	return result[:n], err
}

func (w *webdavReadWriteFile) Write(p []byte) (int, error) {
	reader, err := w.fileWriter()
	if err != nil {
		return 0, err
	}
	n, err := reader.WriteAt(p, w.offset)
	if err != nil {
		return n, err
	}
	w.offset += int64(n)
	return n, nil
}

func (w *webdavReadWriteFile) fileReader() (io.ReaderAt, error) {
	if w.reader != nil {
		return w.reader, nil
	}
	reader, err := w.fs.Read(w.filename)
	if err == nil {
		w.reader = reader
	}
	return reader, err
}

func (w *webdavReadWriteFile) fileWriter() (io.WriterAt, error) {
	if w.writer != nil {
		return w.writer, nil
	}
	writer, err := w.fs.Write(w.filename)
	if err == nil {
		w.writer = writer
	}
	return writer, err
}

func (w *webdavReadWriteFile) Stat() (fs.FileInfo, error) {
	if w.stat == nil {
		stat, err := w.fs.Stat(w.filename)
		if err != nil {
			return nil, err
		}
		w.stat = stat
	}
	return w.stat, nil
}

func (w *webdavReadWriteFile) Close() error {
	if w.reader != nil {
		closer, ok := w.reader.(io.Closer)
		if ok {
			return closer.Close()
		}
	}
	if w.writer != nil {
		closer, ok := w.writer.(io.Closer)
		if ok {
			return closer.Close()
		}
	}
	return nil
}
