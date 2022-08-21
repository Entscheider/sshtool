package sftp

import (
	"errors"
	gosftp "github.com/pkg/sftp"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// PermWrapperFS is a [sftp.SimplifiedFS] that wraps another [sftp.SimplifiedFS] and
// reject read and write operation according to the corresponding regular expression.
type PermWrapperFS struct {
	// The [sftp.SimplifiedFS] to wrap
	Inner SimplifiedFS
	// A list of regular expressions for files/directories that can be read.
	CanReadRegexp []*regexp.Regexp
	// A list of regular expressions for files/directories that can be written.
	CanWriteRegexp []*regexp.Regexp
	// A list of regular expressions for files/directories that should be hidden.
	ShouldHideRegexp []*regexp.Regexp
}

func (p PermWrapperFS) CanRead(path string) bool {
	for _, r := range p.CanReadRegexp {
		if r.MatchString(path) {
			return true
		}
	}
	return false
}

func (p PermWrapperFS) CanWrite(path string) bool {
	for _, r := range p.CanWriteRegexp {
		if r.MatchString(path) {
			return true
		}
	}
	return false
}

// ShouldHide is true iff the given path should be hidden according to the user.
func (p PermWrapperFS) ShouldHide(path string) bool {
	for _, r := range p.ShouldHideRegexp {
		if r.MatchString(path) {
			return true
		}
	}
	return false
}

// Whether the given array contains the given element.
func contains(array []int64, element int64) bool {
	for _, item := range array {
		if item == element {
			return true
		}
	}
	return false
}

func max(a, b int64) int64 {
	if a < b {
		return b
	} else {
		return a
	}
}

func (p PermWrapperFS) List(path string) (func([]os.FileInfo, int64) (int, error), error) {
	if !p.CanRead(path) || p.ShouldHide(path) {
		return nil, ErrForbidden
	}
	iter, err := p.Inner.List(path)
	if err != nil {
		return nil, err
	}
	if len(p.ShouldHideRegexp) == 0 {
		return iter, nil
	}
	// which index among all ls results we certainly know not to show
	var idxToHide []int64
	// which was the highest offset processed yet
	maxOffsetYet := int64(0)
	return func(ls []os.FileInfo, offset int64) (int, error) {
		// We require to filter the inner.List calls:
		// 1. we create a buffer in which we copy the inner result
		raw := make([]os.FileInfo, len(ls))
		// 2. We compute the offset we pass to the inner.List call by checking how many
		//    items we skipped if we go to this offset. The idx to skip are idxToHide and
		//    are supposed to be computed from previous calls
		realOffset := int64(0)
		i := int64(0)
		// TODO: Make this more efficient
		for i < offset {
			// we already know whether the realOffset is one that should be skipped
			if i < maxOffsetYet && !contains(idxToHide, realOffset) {
				// if not to be skipped, we continue
				i += 1
			} else if i >= maxOffsetYet { // we don't know yet if this should be skipped
				maxOffsetYet += 1 // as we process this now, we increment the maxOffsetYet
				// we want to list the single next item
				tmp := make([]os.FileInfo, 1)
				n, err := iter(tmp, realOffset)
				if err != nil {
					return 0, err
				}
				if n == 0 {
					return 0, io.ErrUnexpectedEOF
				}
				// Is this item one we should hide? If so, add it to the idxToHide list
				if p.ShouldHide(filepath.Join(path, tmp[0].Name())) {
					idxToHide = append(idxToHide, realOffset)
				} else {
					// Otherwise, not skipped and continue
					i += 1
				}
			}
			realOffset += 1
		}
		// 3. we now continue and add only those item from the internal result that are viewable
		j := 0 // the number of item copied into ls
		eof := false
		for j == 0 && !eof { // as long as the now item is within ls
			read, err := iter(raw, realOffset) // get the next items
			if err != nil {
				if errors.Is(err, io.EOF) {
					eof = true
				} else {
					return 0, err
				}
			}
			for i := 0; i < read; i++ { // does the filtering
				if !p.ShouldHide(filepath.Join(path, raw[i].Name())) { // should not be hidden -> add it to the ls
					ls[j] = raw[i]
					j += 1
				} else { // should be hidden -> add it to the idxToHide. Don't increment j here
					idx := realOffset + int64(i)
					if !contains(idxToHide, idx) {
						idxToHide = append(idxToHide, idx)
					}
					maxOffsetYet = max(idx, maxOffsetYet)
				}
			}
			realOffset += int64(read) // in the case j == 0, we can continue shift the realOffset
		}
		// j contains the number of items written to ls
		if eof {
			return j, io.EOF
		}
		return j, nil
	}, nil
}

func (p PermWrapperFS) Lstat(path string) (os.FileInfo, error) {
	if p.CanRead(path) && !p.ShouldHide(path) {
		return p.Inner.Lstat(path)
	}
	return nil, ErrForbidden
}

func (p PermWrapperFS) Stat(path string) (os.FileInfo, error) {
	if p.CanRead(path) && !p.ShouldHide(path) {
		return p.Inner.Stat(path)
	}
	return nil, ErrForbidden
}

func (p PermWrapperFS) ReadLink(path string) (os.FileInfo, error) {
	if p.CanRead(path) && !p.ShouldHide(path) {
		return p.Inner.ReadLink(path)
	}
	return nil, ErrForbidden
}

func (p PermWrapperFS) Read(path string) (io.ReaderAt, error) {
	if p.CanRead(path) && !p.ShouldHide(path) {
		return p.Inner.Read(path)
	}
	return nil, ErrForbidden
}

func (p PermWrapperFS) Write(path string) (io.WriterAt, error) {
	if p.CanWrite(path) && !p.ShouldHide(path) {
		return p.Inner.Write(path)
	}
	return nil, ErrForbidden
}

func (p PermWrapperFS) SetStat(path string, flags gosftp.FileAttrFlags, attributes *gosftp.FileStat) error {
	if p.CanWrite(path) && !p.ShouldHide(path) {
		return p.Inner.SetStat(path, flags, attributes)
	}
	return ErrForbidden
}

func (p PermWrapperFS) Rename(src, dst string) error {
	if p.CanWrite(src) && p.CanWrite(dst) && !p.ShouldHide(src) && !p.ShouldHide(dst) {
		return p.Inner.Rename(src, dst)
	}
	return ErrForbidden
}

func (p PermWrapperFS) Rmdir(path string) error {
	if p.CanWrite(path) && !p.ShouldHide(path) {
		return p.Inner.Rmdir(path)
	}
	return ErrForbidden
}

func (p PermWrapperFS) Rm(path string) error {
	if p.CanWrite(path) && !p.ShouldHide(path) {
		return p.Inner.Rm(path)
	}
	return ErrForbidden
}

func (p PermWrapperFS) Mkdir(path string) error {
	if p.CanWrite(path) && !p.ShouldHide(path) {
		return p.Inner.Mkdir(path)
	}
	return ErrForbidden
}

func (p PermWrapperFS) Link(src, dst string) error {
	if p.CanRead(src) && p.CanWrite(dst) && !p.ShouldHide(src) && !p.ShouldHide(dst) {
		return p.Inner.Link(src, dst)
	}
	return ErrForbidden
}

func (p PermWrapperFS) Symlink(src, dst string) error {
	if p.CanRead(src) && p.CanWrite(dst) && !p.ShouldHide(src) && !p.ShouldHide(dst) {
		return p.Inner.Symlink(src, dst)
	}
	return ErrForbidden
}
